// Copyright (c) 2019 Palantir Technologies. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package crd

import (
	"context"
	demandapi "github.com/palantir/k8s-spark-scheduler-lib/pkg/apis/scaler/v1alpha1"
	demandclient "github.com/palantir/k8s-spark-scheduler-lib/pkg/client/clientset/versioned/typed/scaler/v1alpha1"
	ssinformers "github.com/palantir/k8s-spark-scheduler-lib/pkg/client/informers/externalversions"
	"github.com/palantir/k8s-spark-scheduler-lib/pkg/client/informers/externalversions/scaler/v1alpha1"
	"github.com/palantir/pkg/retry"
	werror "github.com/palantir/witchcraft-go-error"
	"github.com/palantir/witchcraft-go-logging/wlog/svclog/svc1log"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	clientcache "k8s.io/client-go/tools/cache"
	"sync"
	"time"
)

const (
	informerSyncRetryCount          = 5
	informerSyncTimeout             = 2 * time.Second
	informerSyncRetryInitialBackoff = 500 * time.Millisecond
)

// LazyDemandInformer checks for Demand CRD existence and creates a
// demand informer if it exists.
type LazyDemandInformer struct {
	informerFactory     ssinformers.SharedInformerFactory
	apiExtensionsClient apiextensionsclientset.Interface
	demandKubeClient    demandclient.ScalerV1alpha1Interface
	ready               chan struct{}
	informer            v1alpha1.DemandInformer
	lock                sync.RWMutex
}

func NewLazyDemandInformer(
	informerFactory ssinformers.SharedInformerFactory,
	apiExtensionsClient apiextensionsclientset.Interface,
	demandKubeClient demandclient.ScalerV1alpha1Interface) *LazyDemandInformer{
	return &LazyDemandInformer{
		informerFactory: informerFactory,
		apiExtensionsClient: apiExtensionsClient,
		demandKubeClient: demandKubeClient,
		ready: make(chan struct{}),
	}
}

// Informer returns the informer instance if it is initialized, returns nil otherwise
func(ldi *LazyDemandInformer) Informer() v1alpha1.DemandInformer {
	ldi.lock.RLock()
	defer ldi.lock.RUnlock()
	return ldi.informer
}

// Ready returns a channel that will be closed when the informer is initialized
func (ldi *LazyDemandInformer) Ready() <-chan struct{} {
	return ldi.ready
}

// Run starts the goroutine to check for the existence of the demand CRD,
// and initialize the demand informer if CRD exists
func (ldi *LazyDemandInformer) Run(ctx context.Context) error {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if ldi.checkDemandCRDExists(ctx) {
				return nil
			}
		}
	}

}

func (ldi *LazyDemandInformer) checkDemandCRDExists(ctx context.Context) bool {
	_, ready, err := CheckCRDExists(demandapi.DemandCustomResourceDefinitionName(), ldi.apiExtensionsClient)
	if err != nil {
		svc1log.FromContext(ctx).Info("failed to determine if demand CRD exists", svc1log.Stacktrace(err))
		return false
	}
	if ready {
		svc1log.FromContext(ctx).Info("demand CRD has been initialized. Demand resources can now be created")
		err = ldi.initializeInformer(ctx)
		if err != nil {
			svc1log.FromContext(ctx).Error("failed initializing demand informer", svc1log.Stacktrace(err))
			return false
		}
	}
	return ready
}

func (ldi *LazyDemandInformer) initializeInformer(ctx context.Context) error {
	ldi.lock.Lock()
	defer ldi.lock.Unlock()
	informerInterface := ldi.informerFactory.Scaler().V1alpha1().Demands()
	informer := informerInterface.Informer()
	ldi.informerFactory.Start(ctx.Done())

	err := retry.Do(ctx, func() error {
		ctxWithTimeout, cancel := context.WithTimeout(ctx, informerSyncTimeout)
		defer cancel()
		if ok := clientcache.WaitForCacheSync(ctxWithTimeout.Done(), informer.HasSynced); !ok {
			return werror.ErrorWithContextParams(ctx,"timeout syncing informer", werror.SafeParam("timeoutSeconds", informerSyncTimeout.Seconds()))
		}
		return nil
	}, retry.WithMaxAttempts(informerSyncRetryCount), retry.WithInitialBackoff(informerSyncRetryInitialBackoff))

	if err != nil {
		return err
	}
	ldi.informer = informerInterface
	return nil
}
