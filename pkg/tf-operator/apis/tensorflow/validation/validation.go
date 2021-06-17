// Copyright 2018 The Kubeflow Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package validation

import (
	"fmt"

	log "github.com/sirupsen/logrus"

	commonv1 "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/apis/common/v1"
	tfv1 "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/apis/tensorflow/v1"
)

// ValidateV1TFJobSpec checks that the v1.TFJobSpec is valid.
func ValidateV1TFJobSpec(c *tfv1.TFJobSpec) error {
	return validateV1ReplicaSpecs(c.TFReplicaSpecs)
}

func validateV1ReplicaSpecs(specs map[commonv1.ReplicaType]*commonv1.ReplicaSpec) error {
	if specs == nil {
		return fmt.Errorf("TFJobSpec is not valid")
	}
	foundChief := 0
	for rType, value := range specs {
		if value == nil || len(value.Template.Spec.Containers) == 0 {
			return fmt.Errorf("TFJobSpec is not valid: containers definition expected in %v", rType)
		}
		if tfv1.IsChieforMaster(rType) {
			foundChief++
		}
		// Make sure the image is defined in the container.
		numNamedTensorflow := 0
		for _, container := range value.Template.Spec.Containers {
			if container.Image == "" {
				msg := fmt.Sprintf("TFJobSpec is not valid: Image is undefined in the container of %v", rType)
				log.Error(msg)
				return fmt.Errorf(msg)
			}
			if container.Name == tfv1.DefaultContainerName {
				numNamedTensorflow++
			}
		}
		// Make sure there has at least one container named "tensorflow".
		if numNamedTensorflow == 0 {
			msg := fmt.Sprintf("TFJobSpec is not valid: There is no container named %s in %v", tfv1.DefaultContainerName, rType)
			log.Error(msg)
			return fmt.Errorf(msg)
		}
	}
	if foundChief > 1 {
		return fmt.Errorf("TFJobSpec is not valid: more than 1 chief/master found")
	}
	return nil
}
