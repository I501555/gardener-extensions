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

package infrastructure

import (
	"context"
	gcpv1alpha1 "github.com/gardener/gardener-extensions/controllers/provider-gcp/pkg/apis/gcp/v1alpha1"
	infrainternal "github.com/gardener/gardener-extensions/controllers/provider-gcp/pkg/internal/infrastructure"
	"github.com/gardener/gardener-extensions/pkg/controller/infrastructure"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/chartrenderer"
	"github.com/gardener/gardener/pkg/operation/terraformer"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type actuator struct {
	client        client.Client
	restConfig    *rest.Config
	chartRenderer chartrenderer.ChartRenderer
}

// NewActuator creates a new infrastructure.Actuator.
func NewActuator() infrastructure.Actuator {
	return &actuator{}
}

// InjectClient implements inject.Client.
func (a *actuator) InjectClient(client client.Client) error {
	a.client = client
	return nil
}

// InjectConfig implements inject.Config.
func (a *actuator) InjectConfig(config *rest.Config) error {
	a.restConfig = config

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	chartRenderer, err := chartrenderer.New(kubeClient)
	if err != nil {
		return err
	}
	a.chartRenderer = chartRenderer

	return nil
}

func (a *actuator) updateProviderStatus(
	ctx context.Context,
	tf *terraformer.Terraformer,
	infra *extensionsv1alpha1.Infrastructure,
	config *gcpv1alpha1.InfrastructureConfig,
) error {
	status, err := infrainternal.ComputeStatus(tf, config)
	if err != nil {
		return err
	}

	infra.Status.ProviderStatus = &runtime.RawExtension{Object: status}
	return a.client.Update(ctx, infra)
}
