/*
Copyright 2022 Cortex Labs, Inc.

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

package clusterconfig

import (
	"testing"

	cr "github.com/cortexlabs/cortex/pkg/lib/configreader"
	"github.com/stretchr/testify/require"
)

func TestValidateCIDRRejectsMalformedInput(t *testing.T) {
	for _, cidr := range []string{"", "not-a-cidr", "10.0.0.1", "10.0.0.0/33"} {
		_, err := validateCIDR(cidr)
		require.Error(t, err, cidr)
	}
}

// Kubernetes 1.37 rejects loadBalancerSourceRanges entries whose address has bits set beyond the
// prefix length, which fails the istio ingress gateway install ~15 minutes into `cortex cluster up`.
// Validating a config the user is about to apply has to catch this up front and name the range to
// write instead.
func TestValidateCIDRWhiteListsRejectsHostBitsBeyondPrefix(t *testing.T) {
	cc := &Config{}
	cc.APILoadBalancerCIDRWhiteList = []string{"217.7.195.253/32", "15.177.50.88/24"}
	err := cc.validateCIDRWhiteLists()
	require.Error(t, err)
	require.Contains(t, err.Error(), APILoadBalancerCIDRWhiteListKey)
	require.Contains(t, err.Error(), "15.177.50.88/24")
	require.Contains(t, err.Error(), "15.177.50.0/24")

	cc = &Config{}
	cc.OperatorLoadBalancerCIDRWhiteList = []string{"10.1.2.3/8"}
	err = cc.validateCIDRWhiteLists()
	require.Error(t, err)
	require.Contains(t, err.Error(), OperatorLoadBalancerCIDRWhiteListKey)
	require.Contains(t, err.Error(), "10.0.0.0/8")
}

func TestValidateCIDRWhiteListsAcceptsCanonicalRanges(t *testing.T) {
	cc := &Config{}
	cc.APILoadBalancerCIDRWhiteList = []string{"0.0.0.0/0", "217.7.195.253/32", "15.177.50.0/24"}
	cc.OperatorLoadBalancerCIDRWhiteList = []string{"192.168.0.0/16"}
	require.NoError(t, cc.validateCIDRWhiteLists())
	require.NoError(t, (&Config{}).validateCIDRWhiteLists())
}

// The stored config of a cluster created before the strict check (e.g. on Kubernetes 1.34) may contain
// ranges with host bits set. Reading it back (cluster info, cluster configure, operator and proxy
// start-up) must keep working; only a config that is about to be applied is checked strictly.
func TestStoredConfigWithNonCanonicalWhiteListStillParses(t *testing.T) {
	stored := []byte(`
cluster_name: butterfly
region: ap-south-1
node_groups:
  - name: gpu-group
    instance_type: g4dn.xlarge
    min_instances: 1
    max_instances: 2
api_load_balancer_cidr_white_list: [217.7.195.253/32, 15.177.50.88/24]
operator_load_balancer_cidr_white_list: [10.1.2.3/8]
`)
	cc := &Config{}
	err := cr.ParseYAMLBytes(cc, FullConfigValidation, stored)
	require.NoError(t, err)
	require.Equal(t, []string{"217.7.195.253/32", "15.177.50.88/24"}, cc.APILoadBalancerCIDRWhiteList)
}
