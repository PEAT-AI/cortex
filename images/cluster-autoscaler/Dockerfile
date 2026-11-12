# Copyright 2022 Cortex Labs, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Upstream image, unmodified. Until cortex 0.45.0 this was built from the PEAT-AI/autoscaler fork
# (scale-up rate limiter). The fork was dropped with k8s 1.37: upstream moved the autoscaler core to
# kubernetes-sigs/cluster-autoscaler, and the limiter (50 nodes/min) never triggers at our cluster size.
FROM registry.k8s.io/autoscaling/cluster-autoscaler:v1.37.0
