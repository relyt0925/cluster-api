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
	"fmt"
	"github.com/pkg/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	"sigs.k8s.io/cluster-api/controllers/mdutil"
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
	if err := r.reconcileOldMachineSetsOnDelete(ctx, oldMSs, allMSs, d); err != nil {
		return err
	}

	if err := r.syncDeploymentStatus(allMSs, newMS, d); err != nil {
		return err
	}

	// Scale up, if we can.
	if err := r.reconcileNewMachineSetOnDelete(ctx, allMSs, newMS, d); err != nil {
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

func (r *MachineDeploymentReconciler) reconcileOldMachineSetsOnDelete(ctx context.Context, oldMSs []*clusterv1.MachineSet, allMSs []*clusterv1.MachineSet, deployment *clusterv1.MachineDeployment) error {
	log := ctrl.LoggerFrom(ctx)
	if deployment.Spec.Replicas == nil {
		return errors.Errorf("spec replicas for MachineDeployment %q/%q is nil, this is unexpected",
			deployment.Namespace, deployment.Name)
	}
	log.V(4).Info("Checking to see if machines have been deleted or are in the process of deleting for old machine sets")
	totalReplicas := mdutil.GetReplicaCountForMachineSets(allMSs)
	scaleDownAmount := totalReplicas - *deployment.Spec.Replicas
	for _, oldMS := range oldMSs {
		if oldMS.Spec.Replicas == nil || *oldMS.Spec.Replicas <= 0 {
			log.V(4).Info(fmt.Sprintf("MachineSet %s fully scaled down", oldMS.Name))
			continue
		}
		updatedReplicaCount := oldMS.Status.Replicas - oldMS.Status.DeletingReplicas
		if updatedReplicaCount < 0 {
			return errors.Errorf("negative updated replica count %d for MachineSet %q/%q, this is unexpected", updatedReplicaCount, oldMS.Namespace, oldMS.Name)
		}
		machineSetScaleDownAmountDueToMachineDeletion := *oldMS.Spec.Replicas - updatedReplicaCount
		if machineSetScaleDownAmountDueToMachineDeletion < 0 {
			log.V(4).Error(errors.Errorf("unexpected negative scale down amount: %d", machineSetScaleDownAmountDueToMachineDeletion), fmt.Sprintf("Error reconciling MachineSet %s", oldMS.Name))
		}
		scaleDownAmount = scaleDownAmount - machineSetScaleDownAmountDueToMachineDeletion
		log.V(4).Info(fmt.Sprintf("Scaling MachineSet %s to %d", oldMS.Name, updatedReplicaCount))
		if err := r.scaleMachineSet(ctx, oldMS, updatedReplicaCount, deployment); err != nil {
			return err
		}
	}
	log.V(4).Info("Finished reconcile of Old MachineSets due to machine deletion. Now analyzing if there's more potential to scale down")
	for _, oldMS := range oldMSs {
		if scaleDownAmount <= 0 {
			break
		}
		if oldMS.Spec.Replicas == nil || *oldMS.Spec.Replicas <= 0 {
			log.V(4).Info(fmt.Sprintf("MachineSet %s fully scaled down", oldMS.Name))
			continue
		}
		updatedReplicaCount := *oldMS.Spec.Replicas
		if updatedReplicaCount >= scaleDownAmount {
			updatedReplicaCount = updatedReplicaCount - scaleDownAmount
			scaleDownAmount = 0
		} else {
			scaleDownAmount = scaleDownAmount - updatedReplicaCount
			updatedReplicaCount = 0
		}
		log.V(4).Info(fmt.Sprintf("Scaling MachineSet %s to %d", oldMS.Name, updatedReplicaCount))
		if err := r.scaleMachineSet(ctx, oldMS, updatedReplicaCount, deployment); err != nil {
			return err
		}
	}
	log.V(4).Info("Finished reconcile of all old MachineSets")
	return nil
}

func (r *MachineDeploymentReconciler) reconcileNewMachineSetOnDelete(ctx context.Context, allMSs []*clusterv1.MachineSet, newMS *clusterv1.MachineSet, deployment *clusterv1.MachineDeployment) error {
	if deployment.Spec.Replicas == nil {
		return errors.Errorf("spec replicas for MachineDeployment %q/%q is nil, this is unexpected",
			deployment.Namespace, deployment.Name)
	}

	if newMS.Spec.Replicas == nil {
		return errors.Errorf("spec replicas for MachineSet %q/%q is nil, this is unexpected",
			newMS.Namespace, newMS.Name)
	}

	if *(newMS.Spec.Replicas) == *(deployment.Spec.Replicas) {
		// Scaling not required.
		return nil
	}

	if *(newMS.Spec.Replicas) > *(deployment.Spec.Replicas) {
		// Scale down.
		err := r.scaleMachineSet(ctx, newMS, *(deployment.Spec.Replicas), deployment)
		return err
	}

	newReplicasCount, err := mdutil.NewMSNewReplicas(deployment, allMSs, newMS)
	if err != nil {
		return err
	}
	err = r.scaleMachineSet(ctx, newMS, newReplicasCount, deployment)

	return nil
}
