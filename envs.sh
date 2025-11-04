#!/bin/bash
# set the default image registry
#export CORTEX_DEV_DEFAULT_IMAGE_REGISTRY="970653281915.dkr.ecr.ap-south-1.amazonaws.com/cortexlabs"
export CORTEX_DEV_DEFAULT_IMAGE_REGISTRY=970653281915.dkr.ecr.ap-south-1.amazonaws.com/ml

# enable api server monitoring in grafana
export CORTEX_DEV_ADD_CONTROL_PLANE_DASHBOARD="true"

# redirect analytics and error reporting to our dev environment
export CORTEX_TELEMETRY_SENTRY_DSN="https://c334df915c014ffa93f2076769e5b334@sentry.io/1848098"
export CORTEX_TELEMETRY_SEGMENT_WRITE_KEY="0WvoJyCey9z1W2EW7rYTPJUMRYat46dl"

# instruct the Python client to use your development CLI binary (update the path to point to your cortex repo)
export CORTEX_CLI_PATH="/home/thomas/projects/cortex/bin/cortex"
export CORTEX_CLUSTER_NAME="cortex-dev"

export AWS_ACCOUNT_ID=970653281915
export AWS_PROFILE=ML-thomas-cortex
export AWS_REGION=ap-south-1
export CORTEX_VERSION=0.45.0
# create a cortex alias which runs your development CLI
alias cortex="$CORTEX_CLI_PATH"
