/*
Copyright 2019 The Kubernetes Authors.

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

// Package postreadymanifests implements the post-ready manifests action
package postreadymanifests

import (
	"strings"

	"sigs.k8s.io/kind/pkg/errors"

	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions"
	"sigs.k8s.io/kind/pkg/cluster/nodeutils"
)

type action struct{}

// NewAction returns a new action for applying post-ready manifests
func NewAction() actions.Action {
	return &action{}
}

// Execute runs the action
func (a *action) Execute(ctx *actions.ActionContext) error {
	if len(ctx.Config.PostReadyManifests) == 0 {
		return nil
	}

	ctx.Status.Start("Applying post-ready manifests 📦")
	defer ctx.Status.End(false)

	allNodes, err := ctx.Nodes()
	if err != nil {
		return err
	}

	node, err := nodeutils.BootstrapControlPlaneNode(allNodes)
	if err != nil {
		return err
	}

	for i, manifest := range ctx.Config.PostReadyManifests {
		cmd := node.Command(
			"kubectl", "--kubeconfig=/etc/kubernetes/admin.conf",
			"apply", "-f", "-",
		)
		cmd.SetStdin(strings.NewReader(manifest))
		if err := cmd.Run(); err != nil {
			return errors.Wrapf(err, "failed to apply post-ready manifest %d", i)
		}
	}

	ctx.Status.End(true)
	return nil
}
