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

package extender

import (
	"context"

	"github.com/palantir/k8s-spark-scheduler/internal/cache"
	"github.com/palantir/k8s-spark-scheduler/internal/common/utils"
	v1 "k8s.io/api/core/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	clientcache "k8s.io/client-go/tools/cache"
)

// DemandGC is a background pod event handler which deletes any demand we have previously created for a pod when a pod gets scheduled.
// We also delete demands elsewhere in the extender when we schedule the pod, but those can miss some demands due to race conditions.
type DemandGC struct {
	demandCache *cache.SafeDemandCache
	ctx         context.Context
}

// StartDemandGC initializes the DemandGC which handles events in the background
func StartDemandGC(ctx context.Context, podInformer coreinformers.PodInformer, demandCache *cache.SafeDemandCache) {
	dgc := &DemandGC{
		demandCache: demandCache,
		ctx:         ctx,
	}

	podInformer.Informer().AddEventHandler(
		clientcache.FilteringResourceEventHandler{
			FilterFunc: utils.IsSparkSchedulerPod,
			Handler: clientcache.ResourceEventHandlerFuncs{
				UpdateFunc: utils.OnPodScheduled(ctx, func(pod *v1.Pod) {
					DeleteDemandIfExists(dgc.ctx, dgc.demandCache, pod, "DemandGC")
				}),
			},
		},
	)
}
