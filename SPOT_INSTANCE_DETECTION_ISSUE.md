# Spot Instance Detection Issue - AL2023 / K8s 1.34 Upgrade

## Problem Statement

After upgrading to Kubernetes 1.34 and Amazon Linux 2023, spot instance detection is broken. The `cortex cluster info` command and Grafana cost metrics incorrectly show **all instances in mixed instance groups as spot**, even when they are on-demand.

### Observed Behavior
```
cortex cluster info --name butterfly --region ap-south-1

# Shows BOTH g4dn.xlarge instances as spot:
instance type   lifecycle   replicas
g4dn.xlarge     spot        1          # WRONG - this is actually on-demand
g4dn.xlarge     spot        1          # Correct - this is spot
t3.medium       on-demand   1          # Correct
```

### Actual AWS State
```bash
aws ec2 describe-instances --region ap-south-1 --instance-ids i-xxx i-yyy

# Shows correct lifecycle:
i-048ab1fda10b3c15f  g4dn.xlarge  None    # on-demand (InstanceLifecycle=null)
i-0cde17c36a8bc77dc  g4dn.xlarge  spot    # spot (InstanceLifecycle="spot")
```

## Root Cause

### Amazon Linux 2 (Old Behavior)
- Used `bootstrap.sh` script for node initialization
- Automatically set **per-instance** label: `node-lifecycle=spot` for spot instances
- Operator correctly detected lifecycle via this label

### Amazon Linux 2023 (New Behavior)
- Uses `nodeadm` with NodeConfig YAML for initialization
- Does **NOT** automatically set per-instance `node-lifecycle` label
- Only has **nodegroup-level** label: `lifecycle=Ec2Spot` (set by `generate_eks.py`)
- This nodegroup label applies to **ALL nodes** in a mixed instance group, regardless of actual lifecycle

### Code Issue
Operator checks for labels:
```go
// pkg/operator/endpoints/info.go:78
isSpot := node.Labels["lifecycle"] == "Ec2Spot"  // WRONG: This is a nodegroup label!
```

Result: All nodes in spot-enabled nodegroups are detected as spot, even if they're actually on-demand.

---

## Attempted Solutions

### Attempt 1: Dynamic Lifecycle Detection via Node Bootstrap

**Approach:** Query EC2 Instance Metadata Service (IMDS) at node boot time to detect lifecycle and set as kubelet label.

**Implementation:**
```python
# manager/generate_eks.py
"overrideBootstrapCommand": "\n".join([
    "#!/bin/bash",
    "# Query IMDS for lifecycle",
    'LIFECYCLE=$(curl http://169.254.169.254/latest/meta-data/instance-life-cycle)',
    '[ "$LIFECYCLE" = "spot" ] && LABEL="spot" || LABEL="on-demand"',
    # Generate NodeConfig with dynamic label...
])
```

**Result:** ❌ **FAILED**

**Error:**
```
could not add resources for nodegroup: unmarshalling "overrideBootstrapCommand"
into "nodeadm.NodeConfig": json: cannot unmarshal string into Go value of type v1alpha1.NodeConfig
```

**Why it failed:**
- AL2023's `overrideBootstrapCommand` expects **static NodeConfig YAML**, not a bash script
- Cannot use template variables (like `{{.NodeLabels}}`) - they don't get substituted ([commit d1b01403](https://github.com/PEAT-AI/cortex/commit/d1b01403))
- eksctl generates the first NodeConfig with proper labels/taints automatically
- Our override should only provide kubelet config, not attempt to generate dynamic content

**Key limitation discovered:**
> AL2023's bootstrap system requires static configuration. You cannot dynamically determine values (like lifecycle) at boot time and inject them into kubelet flags via NodeConfig.

---

## Proposed Solution: EC2 API Query from Operator

### Overview
Instead of trying to detect lifecycle at node boot time, query the EC2 API from the operator to determine the actual instance lifecycle.

### Approach
```go
// pkg/lib/aws/ec2.go
func (c *Client) IsSpotInstance(instanceID string) (bool, error) {
    result, err := c.EC2().DescribeInstances(&ec2.DescribeInstancesInput{
        InstanceIds: []*string{aws.String(instanceID)},
    })
    if err != nil {
        return false, errors.Wrap(err, "checking instance lifecycle")
    }

    if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
        return false, errors.New("instance not found")
    }

    instance := result.Reservations[0].Instances[0]
    // InstanceLifecycle is "spot" for spot instances, nil for on-demand
    return instance.InstanceLifecycle != nil && *instance.InstanceLifecycle == "spot", nil
}
```

### Changes Required

#### 1. Add EC2 API Helper
**File:** `pkg/lib/aws/ec2.go`
- Add `IsSpotInstance(instanceID string)` method to check actual instance lifecycle
- Uses `DescribeInstances` EC2 API call
- Caches results with 10-minute TTL to minimize API calls

#### 2. Update Operator Info Endpoint
**File:** `pkg/operator/endpoints/info.go:59-108`
```go
func getWorkerNodeInfos() ([]schema.WorkerNodeInfo, int, error) {
    // ... existing code ...

    spotInstanceCache := make(map[string]bool) // instanceID -> isSpot

    for i := range nodes {
        node := nodes[i]

        // Extract instance ID from providerID: aws:///zone/i-xxxxx
        instanceID := extractInstanceID(node.Spec.ProviderID)

        isSpot := false
        if cachedValue, ok := spotInstanceCache[instanceID]; ok {
            isSpot = cachedValue
        } else {
            isSpot, err = config.AWS.IsSpotInstance(instanceID)
            if err != nil {
                // Log error but don't fail - fallback to label check
                isSpot = node.Labels["lifecycle"] == "Ec2Spot"
            }
            spotInstanceCache[instanceID] = isSpot
        }

        // ... rest of existing code ...
    }
}
```

#### 3. Update Cost Breakdown Cron
**File:** `pkg/operator/operator/cron.go`
- Update `ClusterTelemetry()` function (line 130)
- Update `CostBreakdown()` function (line 306)
- Same pattern: query EC2 API with caching

#### 4. Add IAM Permission
**File:** `pkg/types/clusterconfig/aws_policy.go:38-52`
```json
{
    "Action": [
        "sts:GetCallerIdentity",
        "ecr:GetAuthorizationToken",
        "ecr:BatchGetImage",
        "sqs:ListQueues",
        "ec2:DescribeSpotPriceHistory",
        "ec2:DescribeInstances"  // ADD THIS
    ],
    "Effect": "Allow",
    "Resource": "*"
}
```

### Advantages
✅ Works with AL2023's static NodeConfig requirements
✅ No node-level scripting complexity
✅ Accurate detection even in mixed instance groups
✅ Minimal API calls with caching (~72 calls/hour max for 6 nodes)
✅ Graceful fallback if EC2 API fails

### Disadvantages
⚠️ Requires IAM policy update: `cortex cluster configure`
⚠️ Adds dependency on EC2 DescribeInstances permission
⚠️ Slight increase in API calls (within AWS limits)

---

## Alternative Solutions Considered

### Alt 1: Use DaemonSet to Apply Labels Post-Boot
Run a DaemonSet that queries IMDS and applies the label after nodes join.

**Pros:** No IAM permission changes needed
**Cons:** Complex, requires additional Kubernetes resources, label application delayed

### Alt 2: Switch to EKS Managed Node Groups
Use managed node groups which automatically get `eks.amazonaws.com/capacityType=SPOT` label.

**Pros:** AWS handles everything
**Cons:** Major infrastructure change, loses fine-grained control

### Alt 3: Accept Inaccurate Detection
Keep current behavior, document limitation.

**Pros:** No code changes
**Cons:** Broken cost metrics, misleading cluster info

---

## Recommendation

**Implement the EC2 API query solution (Proposed Solution above).**

This is the cleanest approach that:
1. Works within AL2023's constraints
2. Provides accurate detection
3. Has minimal performance impact
4. Requires only a small IAM policy update

### Implementation Steps

1. **Add EC2 API method** (`pkg/lib/aws/ec2.go`)
2. **Update operator info endpoint** (`pkg/operator/endpoints/info.go`)
3. **Update cost cron jobs** (`pkg/operator/operator/cron.go`)
4. **Update IAM policy** (`pkg/types/clusterconfig/aws_policy.go`)
5. **Fix optional field handling** (`manager/generate_eks.py` - keep the optional field checks)
6. **Test on dev cluster**
7. **Update IAM**: Run `cortex cluster configure` to apply new permissions
8. **Rebuild operator**: `make operator-update`
9. **Verify**: Check `cortex cluster info` shows correct lifecycle

---

## References

- [Commit d1b01403](https://github.com/PEAT-AI/cortex/commit/d1b01403): Why template variables don't work in AL2023
- [Commit 561963a8](https://github.com/PEAT-AI/cortex/commit/561963a8): AL2023 migration
- [eksctl PR #8078](https://github.com/weaveworks/eksctl/pull/8078): AL2023 overrideBootstrapCommand support
- [eksctl PR #8031](https://github.com/weaveworks/eksctl/pull/8031): AL2023 preBootstrapCommands support
- [EKS AMI Issue #1963](https://github.com/awslabs/amazon-eks-ami/issues/1963): AL2023 bootstrap discussion

---

**Status:** Awaiting decision to proceed with EC2 API solution
**Date:** 2025-11-12
**Affected Clusters:** butterfly, butterfly2 (all clusters on K8s 1.34 / AL2023)
