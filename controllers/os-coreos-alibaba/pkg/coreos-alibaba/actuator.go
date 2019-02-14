// Copyright (c) 2019 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package coreos

import (
	"context"
	"fmt"
	"github.com/gardener/gardener-extensions/controllers/os-coreos-alibaba/pkg/coreos-alibaba/internal"
	"github.com/gardener/gardener-extensions/controllers/os-coreos-alibaba/pkg/coreos-alibaba/internal/cloudinit"
	"github.com/gardener/gardener-extensions/pkg/controller"
	"github.com/gardener/gardener-extensions/pkg/controller/operatingsystemconfig"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Type is the type of operating system configs the CoreOS Alibaba controller monitors.
const Type = "coreos-alibaba"

type actuator struct {
	scheme *runtime.Scheme
	client client.Client
	logger logr.Logger
}

// NewActuator creates a new actuator with the given logger.
func NewActuator(logger logr.Logger) operatingsystemconfig.Actuator {
	return &actuator{
		logger: logger,
	}
}

func (a *actuator) InjectScheme(scheme *runtime.Scheme) error {
	a.scheme = scheme
	return nil
}

func (a *actuator) InjectClient(client client.Client) error {
	a.client = client
	return nil
}

func (a *actuator) Exists(ctx context.Context, config *extensionsv1alpha1.OperatingSystemConfig) (bool, error) {
	return config.Status.CloudConfig != nil, nil
}

func (a *actuator) Create(ctx context.Context, config *extensionsv1alpha1.OperatingSystemConfig) error {
	return a.reconcile(ctx, config)
}

func (a *actuator) Update(ctx context.Context, config *extensionsv1alpha1.OperatingSystemConfig) error {
	return a.reconcile(ctx, config)
}

func (a *actuator) Delete(ctx context.Context, config *extensionsv1alpha1.OperatingSystemConfig) error {
	return a.delete(ctx, config)
}

func (a *actuator) reconcile(ctx context.Context, config *extensionsv1alpha1.OperatingSystemConfig) error {
	cloudConfig, err := a.cloudConfigFromOperatingSystemConfig(ctx, config)
	if err != nil {
		config.Status.ObservedGeneration = config.Generation
		config.Status.LastOperation, config.Status.LastError = controller.ReconcileError(extensionsv1alpha1.LastOperationTypeReconcile, fmt.Sprintf("Could not generate cloud config: %v", err), 50)
		if err := a.client.Status().Update(ctx, config); err != nil {
			a.logger.Error(err, "Could not update operating system config status after update error", "osc", config.Name)
		}
		return err
	}

	secret := &corev1.Secret{
		ObjectMeta: secretObjectMetaForConfig(config),
	}

	if err := controller.CreateOrUpdate(ctx, a.client, secret, func() error {
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data[extensionsv1alpha1.OperatingSystemConfigSecretDataKey] = []byte(cloudConfig)

		return controllerutil.SetControllerReference(config, secret, a.scheme)
	}); err != nil {
		config.Status.ObservedGeneration = config.Generation
		config.Status.LastOperation, config.Status.LastError = controller.ReconcileError(extensionsv1alpha1.LastOperationTypeReconcile, fmt.Sprintf("Could not apply secret for generated cloud config: %v", err), 50)
		if err := a.client.Status().Update(ctx, config); err != nil {
			a.logger.Error(err, "Could not update operating system config status after reconcile error", "osc", config.Name)
		}
		return err
	}

	config.Status.CloudConfig = &extensionsv1alpha1.CloudConfig{
		SecretRef: corev1.SecretReference{
			Name:      secret.Name,
			Namespace: secret.Namespace,
		},
	}
	config.Status.ObservedGeneration = config.Generation
	config.Status.LastOperation, config.Status.LastError = controller.ReconcileSucceeded(extensionsv1alpha1.LastOperationTypeReconcile, "Successfully generated cloud config")
	return a.client.Status().Update(ctx, config)
}

func (a *actuator) delete(ctx context.Context, config *extensionsv1alpha1.OperatingSystemConfig) error {
	config.Status.ObservedGeneration = config.Generation
	config.Status.LastOperation, config.Status.LastError = controller.ReconcileSucceeded(extensionsv1alpha1.LastOperationTypeDelete, "Successfully deleted cloud config")
	if err := a.client.Status().Update(ctx, config); err != nil {
		a.logger.Error(err, "Could not update operating system config status for deletion", "osc", config.Name)
		return err
	}
	return nil
}

func secretObjectMetaForConfig(config *extensionsv1alpha1.OperatingSystemConfig) metav1.ObjectMeta {
	var (
		name      = fmt.Sprintf("osc-result-%s", config.Name)
		namespace = config.Namespace
	)

	if cloudConfig := config.Status.CloudConfig; cloudConfig != nil {
		name = cloudConfig.SecretRef.Name
		namespace = cloudConfig.SecretRef.Namespace
	}

	return metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
	}
}

func (a *actuator) dataForFileContent(ctx context.Context, namespace string, content *extensionsv1alpha1.FileContent) ([]byte, error) {
	if inline := content.Inline; inline != nil {
		if len(inline.Encoding) == 0 {
			return []byte(inline.Data), nil
		}
		return cloudinit.Decode(inline.Encoding, []byte(inline.Data))
	}

	key := client.ObjectKey{Namespace: namespace, Name: content.SecretRef.Name}
	secret := &corev1.Secret{}
	if err := a.client.Get(ctx, key, secret); err != nil {
		return nil, err
	}
	return secret.Data[content.SecretRef.DataKey], nil
}

func (a *actuator) cloudConfigFromOperatingSystemConfig(ctx context.Context, config *extensionsv1alpha1.OperatingSystemConfig) ([]byte, error) {
	files := make([]*internal.File, 0, len(config.Spec.Files))
	for _, file := range config.Spec.Files {
		data, err := a.dataForFileContent(ctx, config.Namespace, &file.Content)
		if err != nil {
			return nil, err
		}

		files = append(files, &internal.File{Path: file.Path, Content: data, Permissions: file.Permissions})
	}

	units := make([]*internal.Unit, 0, len(config.Spec.Units))
	for _, unit := range config.Spec.Units {
		var content []byte
		if unit.Content != nil {
			content = []byte(*unit.Content)
		}

		dropIns := make([]*internal.DropIn, 0, len(unit.DropIns))
		for _, dropIn := range unit.DropIns {
			dropIns = append(dropIns, &internal.DropIn{Name: dropIn.Name, Content: []byte(dropIn.Content)})
		}
		units = append(units, &internal.Unit{Name: unit.Name, Content: content, DropIns: dropIns})
	}

	return internal.NewCloudInitGenerator(internal.DefaultUnitsPath).Generate(&internal.OperatingSystemConfig{Files: files, Units: units})
}
