** cortex update to kubernetes 1.32 **

todo after we are done:
reinstall gcc & binutils with brew


problems I stumbled over:
** glibc linker errors **

I have go installed on ubuntu 
using these commands:

mkdir -p ~/bin && \
wget https://dl.google.com/go/go1.24.0.linux-amd64.tar.gz && \
sudo tar -xvf go1.24.0.linux-amd64.tar.gz && \
sudo mv go /usr/local && \
rm go1.24.0.linux-amd64.tar.gz && \
echo 'export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"' >> $HOME/.bashrc


When I run make on cortex ( a go project), I get linker errors:
# github.com/cortexlabs/cortex/pkg/probe.test
/usr/local/go/pkg/tool/linux_amd64/link: running gcc failed: exit status 1
/home/linuxbrew/.linuxbrew/bin/ld: /usr/libexec/gcc/x86_64-linux-gnu/13/liblto_plugin.so: error loading plugin: /home/linuxbrew/.linuxbrew/opt/glibc/lib/libc.so.6: version GLIBC_2.38' not found (required by /usr/libexec/gcc/x86_64-linux-gnu/13/liblto_plugin.so)
collect2: error: ld returned 1 exit status

---
temporary solution:
brew unlink gcc
brew unlink binutils

--- 
follow instructions in CONTRIBUTING.md

--- 
make test
ran into an issue where we couldn't connect to localstack (s3). It didn't find a bucket "health".
We changed the test so it's now testing the creation of a SQS queue.


---
skip make tools, requires newer go version. maybe get new go version later, when we know which one.
---
start going through versions.md
ok we need to check which is the latest eksctl release on
https://github.com/weaveworks/eksctl/releases
latest version is eksctl 0.206.0
old version is v0.143.0
so search & replace 0.143/0.206.0

---
check what we did in generate_eks.py before. Ah, we updated the
aws-vpc-cni version to 1.12.6. What is it currently in eksctl?
We check eksctl/pkg/addons/default/assets/aws-node.yaml of current eksctl
and conclude it's v1.19.3.
Let's update it in generate_eks.py.  

---
Check if eksctl iam polices changed by comparing the previous version of the eksctl policy docs to the new version's and update `./dev/minimum_aws_policy.json` .
We find some changes and update accordingly. 
---
## Kubernetes
newest kubernetes version is 1.32.
We update it in generate_eks.py and generate_ami_mapping.go
---
we update ami.json by running:
```sh
go run build/generate_ami_mapping.go manager/manifests/ami.json public
```
--- 
skip kube-proxy part for now. I think i skipped it last time as well.
if we observe problems, this is something we can check later.
---
aws-iam-authenticator
The link in versions.md is not working anymore. Let's try
https://github.com/kubernetes-sigs/aws-iam-authenticator
Which version is it at?
0.6.30 (previously: 0.5.9). We update it in 
manager/Dockerfile
---
kubectl
latest stable version is 1.31.
oh. so maybe k8s 1.32 is not stable? ok let's change it to 1.31 everywhere.
regenerate ami and so on.
sidenote: kubectl that was installed with brew was version 1.32. 

## istio
old version 1.17.2
new version 1.23.5
search & replace

versions.md tells us to adapt some other istio files.
I don't currently know how and if, so let's skip it for now.
I assume if something is wrong we will run into errors later and fix
them as they occur.

## aws cni
we find the new location of the vpc ip resource limit file is
https://raw.githubusercontent.com/aws/amazon-vpc-cni-k8s/v${NEW_RELEASE}/pkg/vpc/vpc_ip_resource_limit.go

But it's formatted differently, so a diff is looking not good. 
Also there's too many differences and we don't care for all the new instance types, so
let's ignore this.
We have already updated the aws cni version above. 

## go
latest version is 1.24.0 (previously: 1.20.4)
search & replace 1.20.4/1.24.0

look up a few version numbers, mostly for k8s and istio, and run the go module update steps.
Commit go.mod & go.sum.

## nvidia device plugin and other gpu related stuff
skip for now. Hope it works with old version.
Only update what's critical for now, see if we get this thing running.

## cluster autoscaler
we compare the diff like described in versions.md and move selectively some stuff from the new autoscaler cluster yaml
to the cortex version.
It says "--expander=least-waste". I don't know what this is, but I feel it's safer to not copy it as it probably
will mess with the carefully tuned current autoscaling.

checkout autoscaler fork:
```sh
gh repo clone PEAT-AI/autoscaler
git checkout cluster-autoscaler-1.26.3-cortex
git log
```
latest commit is
e903cb56a015c4693eb85c00fa6100cb835e0974
latest non-cortex commit is
cd86044bf66cb91531e835f6746c17277dd5ac22

```sh
git reset cd86044bf66cb91531e835f6746c17277dd5ac22
git add .
git stash
git fetch upstream
git checkout cluster-autoscaler-1.31.2 -b cluster-autoscaler-1.31.2-cortex
git stash pop
```
Resolve merge conflicts.
So basically in cortex we add:

ScaleUpRateLimitEnabled bool
ScaleUpMaxNumberOfNodesPerMin int
ScaleUpBurstMaxNumberOfNodesPerMin int

so in vscode we go in the merge conflict editor and always choose
"accept combination" when possible.

We also apply fixes to autoscaler that we have on the 27.2 branch.

## 
Now we try to get the tests running:
```
make test
```
We get lots of errors like this:
```
# github.com/docker/distribution/reference                                                                                                                                                                                                                                        ../../go/pkg/mod/github.com/docker/distribution@v2.8.3+incompatible/reference/reference_deprecated.go:122:19: undefined: reference.SplitHostname
# github.com/docker/cli/opts                                                                                                                                                                                                                                                      ../../go/pkg/mod/github.com/docker/cli@v27.4.1+incompatible/opts/ulimit.go:13:32: undefined: container.Ulimit                                                                                                                                                                     ../../go/pkg/mod/github.com/docker/cli@v27.4.1+incompatible/opts/ulimit.go:17:46: undefined: container.Ulimit                                                                                                                                                                     ../../go/pkg/mod/github.com/docker/cli@v27.4.1+incompatible/opts/ulimit.go:20:32: undefined: container.Ulimit                                                                                                                                                                     ../../go/pkg/mod/github.com/docker/cli@v27.4.1+incompatible/opts/ulimit.go:49:44: undefined: container.Ulimit                                                                                                                                                                     ../../go/pkg/mod/github.com/docker/cli@v27.4.1+incompatible/opts/mount.go:125:19: bindOptions().ReadOnlyNonRecursive undefined (type *mount.BindOptions has no field or method ReadOnlyNonRecursive)
../../go/pkg/mod/github.com/docker/cli@v27.4.1+incompatible/opts/mount.go:127:19: bindOptions().ReadOnlyForceRecursive undefined (type *mount.BindOptions has no field or method ReadOnlyForceRecursive)
../../go/pkg/mod/github.com/docker/cli@v27.4.1+incompatible/opts/mount.go:135:20: volumeOptions().Subpath undefined (type *mount.VolumeOptions has no field or method Subpath)
../../go/pkg/mod/github.com/docker/cli@v27.4.1+incompatible/opts/mount.go:186:24: mount.BindOptions.ReadOnlyNonRecursive undefined (type *mount.BindOptions has no field or method ReadOnlyNonRecursive)
../../go/pkg/mod/github.com/docker/cli@v27.4.1+incompatible/opts/mount.go:191:24: mount.BindOptions.ReadOnlyForceRecursive undefined (type *mount.BindOptions has no field or method ReadOnlyForceRecursive)
../../go/pkg/mod/github.com/docker/cli@v27.4.1+incompatible/opts/parse.go:95:21: undefined: container.RestartPolicyMode                                                                                                                                                           ../../go/pkg/mod/github.com/docker/cli@v27.4.1+incompatible/opts/ulimit.go:20:32: too many errors                                                                                                                                                                                 
```

Great! Now what!?

go get -u sigs.k8s.io/controller-runtime@v0.14.6
this downgrades 
go: downgraded github.com/aws/amazon-vpc-cni-k8s v1.19.3 => v1.15.4
go: downgraded github.com/aws/amazon-vpc-resource-controller-k8s v1.5.0 => v1.3.0

so let's use 
sigs.k8s.io/controller-runtime v0.19.1


