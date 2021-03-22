/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"github.com/pkg/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	"sigs.k8s.io/cluster-api/controllers/mdutil"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
)

// rolloutRolling ....
func (r *MachineDeploymentReconciler) onDeleteUpgrade(ctx context.Context, d *clusterv1.MachineDeployment, msList []*clusterv1.MachineSet) error {
	newMS, oldMSs, err := r.getAllMachineSetsAndSyncRevision(ctx, d, msList, true)
	if err != nil {
		return err
	}

	// newMS can be nil in case there is already a MachineSet associated with this deployment,
	// but there are only either changes in annotations or MinReadySeconds. Or in other words,
	// this can be nil if there are changes, but no replacement of existing machines is needed.
	if newMS == nil {
		return nil
	}

	allMSs := append(oldMSs, newMS)

	// Scale down, if we can.
	if err := r.reconcileOldMachineSetsOnDelete(ctx, oldMSs, d); err != nil {
		return err
	}

	if err := r.syncDeploymentStatus(allMSs, newMS, d); err != nil {
		return err
	}

	// Scale up, if we can.
	if err := r.reconcileNewMachineSetOnDelete(ctx, oldMSs, newMS, d); err != nil {
		return err
	}

	if err := r.syncDeploymentStatus(allMSs, newMS, d); err != nil {
		return err
	}

	if mdutil.DeploymentComplete(d, &d.Status) {
		if err := r.cleanupDeployment(ctx, oldMSs, d); err != nil {
			return err
		}
	}

	return nil
}

func (r *MachineDeploymentReconciler) reconcileOldMachineSetsOnDelete(ctx context.Context, oldMSs []*clusterv1.MachineSet, deployment *clusterv1.MachineDeployment) error {
	log := ctrl.LoggerFrom(ctx)
	log.V(4).Info("HI")

	if deployment.Spec.Replicas == nil {
		return errors.Errorf("spec replicas for MachineDeployment %q/%q is nil, this is unexpected",
			deployment.Namespace, deployment.Name)
	}

	for _, oldMS := range oldMSs {
		if oldMS.Spec.Replicas == nil || *oldMS.Spec.Replicas == 0 {
			//already scaled down all the way
			continue
		}
		newReplicaCount := oldMS.Status.Replicas - oldMS.Status.DeletingReplicas
		if newReplicaCount < 0 {
			//error
			continue
		}
		if newReplicaCount < *oldMS.Spec.Replicas {
			patchHelper, err := patch.NewHelper(oldMS, r.Client)
			if err != nil {
				return err
			}
			oldMS.Spec.Replicas = &newReplicaCount
			err = patchHelper.Patch(ctx, oldMS)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *MachineDeploymentReconciler) reconcileNewMachineSetOnDelete(ctx context.Context, oldMSs []*clusterv1.MachineSet, newMS *clusterv1.MachineSet, deployment *clusterv1.MachineDeployment) error {
	log := ctrl.LoggerFrom(ctx)
	log.V(4).Info("HI")

	if deployment.Spec.Replicas == nil {
		return errors.Errorf("spec replicas for MachineDeployment %q/%q is nil, this is unexpected",
			deployment.Namespace, deployment.Name)
	}

	if newMS.Spec.Replicas == nil {
		return errors.Errorf("spec replicas for MachineSet %q/%q is nil, this is unexpected",
			newMS.Namespace, newMS.Name)
	}

	totalOldReplicas := mdutil.GetActualReplicaCountForMachineSets(oldMSs)
	availableSizeUpForNewReplicas := *deployment.Spec.Replicas - *newMS.Spec.Replicas - totalOldReplicas
	if availableSizeUpForNewReplicas > 0 {
		newSize := *newMS.Spec.Replicas + availableSizeUpForNewReplicas
		patchHelper, err := patch.NewHelper(newMS, r.Client)
		if err != nil {
			return err
		}
		newMS.Spec.Replicas = &newSize
		err = patchHelper.Patch(ctx, newMS)
		if err != nil {
			return err
		}
	}
	return nil
}
