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

	"github.com/stretchr/testify/require"
)

func TestValidateCIDRAcceptsCanonicalRanges(t *testing.T) {
	for _, cidr := range []string{"0.0.0.0/0", "217.7.195.253/32", "15.177.50.0/24", "192.168.0.0/16"} {
		got, err := validateCIDR(cidr)
		require.NoError(t, err, cidr)
		require.Equal(t, cidr, got)
	}
}

// Kubernetes 1.37 rejects loadBalancerSourceRanges entries whose address has bits set beyond the
// prefix length, which fails the istio ingress gateway install ~15 minutes into `cortex cluster up`.
// The cluster config validation has to catch this up front and name the range to write instead.
func TestValidateCIDRRejectsHostBitsBeyondPrefix(t *testing.T) {
	_, err := validateCIDR("15.177.50.88/24")
	require.Error(t, err)
	require.Contains(t, err.Error(), "15.177.50.88/24")
	require.Contains(t, err.Error(), "15.177.50.0/24")
}

func TestValidateCIDRRejectsMalformedInput(t *testing.T) {
	for _, cidr := range []string{"", "not-a-cidr", "10.0.0.1", "10.0.0.0/33"} {
		_, err := validateCIDR(cidr)
		require.Error(t, err, cidr)
	}
}
