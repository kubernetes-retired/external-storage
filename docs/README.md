# Docs
* External provisioner library
	* [`hostPath` demo](demo/hostpath-provisioner/README.md) - a comprehensive walkthrough of how to use the library to write and build then run a `hostPath` provisioner (on a local one-node cluster)
	* More in-depth looks at particular topics:
		* [Building provisioner programs and managing dependencies](#building-provisioner-programs-and-managing-dependencies)
		* [Authorizing provisioners for RBAC or OpenShift](#authorizing-provisioners-for-rbac-or-openshift)
		* [Running multiple provisioners and giving provisioners identities](#running-multiple-provisioners-and-giving-provisioners-identities)
	* [The code](../lib/controller) - being a library, the code is *supposed* to be well-documented -- if you find it insufficient, open an issue
* [Contributing](#contributing)

## Building provisioner programs and managing dependencies

The library depends on [client-go](https://github.com/kubernetes/client-go) and your provisioner probably will too. This situation pretty much necessitates that you manage your dependencies with [vendoring](https://github.com/golang/go/wiki/PackageManagementTools) using a tool like [glide](https://github.com/Masterminds/glide).

Please see [client-go's installation doc](https://github.com/kubernetes/client-go/blob/master/INSTALL.md#installing-client-go) for a good explanation on how to depend on client-go and dependency management in general.

Let's say you've just finished writing your prototype provisioner. Now you want to vendor its dependencies using glide so that you can compile your program using the dependencies.

Your program's imports will probably include packages like these:

```go
import (
...
	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
)
```

Obviously the external provisioner library is there. So too are client-go and apimachinery, because they provide packages essential to applications made for Kubernetes.

Run `glide init` to populate a glide.yaml. When asked about using a release of external-storage answer Yes! But when asked about client-go or apimachinery, answer **No**! The reason you say No here is because external-storage depends on specific versions of these repos, and glide is not smart enough to always make the correct recommendation here.

```
[INFO]	The package github.com/kubernetes-incubator/external-storage appears to have Semantic Version releases (http://semver.org).
[INFO]	The latest release is v2.0.0. You are currently not using a release. Would you like
[INFO]	to use this release? Yes (Y) or No (N)
```

(If you ignore glide's prompts, you can always add `version` fields to your glide.yaml yourself later.)

Your glide.yaml will now look like this:

```yaml
package: github.com/kubernetes-incubator/external-storage/docs/demo/hostpath-provisioner
import:
- package: github.com/golang/glog
- package: github.com/kubernetes-incubator/external-storage
  version: v2.0.0
  subpackages:
  - lib/controller
- package: k8s.io/apimachinery
  subpackages:
  - pkg/apis/meta/v1
  - pkg/util/wait
- package: k8s.io/client-go
  subpackages:
  - kubernetes
  - pkg/api/v1
  - rest
```

At this point, if you run `glide install -v` glide *should* be smart enough to determine the correct versions of client-go/apimachinery to fetch, i.e. the versions that can satisfy both your and your other dependencies' (external-storage) requirements. But this is not a guarantee, so for your convenience, external-storage will always specify exactly what version of client-go/apimachinery to use on the [releases page](https://github.com/kubernetes-incubator/external-storage/releases). So add `version` fields to both client-go and apimachinery accordingly.

After you have edited your glide.yaml to your satisfaction, run `glide install -v` to get a vendor directory full of your dependencies which you can build your provisioner with.

Finally you'll want to build your program. You can write some sort of containerized build or stick to a `go build` invocation. In order for a `go build .` or variation thereof to work, you must
* be working in your `GOPATH`, your code has to be somewhere under "$GOPATH/src". This is a requirement (even) when using vendored dependencies
* have go version 1.8 or greater installed
The binary produced can then be e.g. used to make a Docker image.

## Authorizing provisioners for RBAC or OpenShift

The controller requires authorization to perform the following API calls:
* `get`, `list`, `watch`, `create`, `delete` "persistentvolumes"
* `get`, `list`, `watch`, `update` "persistentvolumeclaims"
* `get`, `list`, `watch` "storageclasses"
* `list`, `watch`, `create`, `update`, `patch` "events"

As of Kubernetes 1.6 these needed permissions are enumerated in an RBAC bootstrap `ClusterRole` named ["system:persistent-volume-provisioner"](https://github.com/kubernetes/kubernetes/blob/4e01d1d1412950250148d25ca607fb9585f4c86b/plugin/pkg/auth/authorizer/rbac/bootstrappolicy/testdata/cluster-roles.yaml#L693). In OpenShift this bootstrap `ClusterRole` doesn't yet exist but it would look exactly the same except for the `apiVersion` field.

As the author of your external provisioner you will need to instruct users on how to authorize the provisioner. Assuming you intend for the provisioner to be deployed as an application on top of Kubernetes/OpenShift, authorization means creating a service account for the provisioner to run as and granting the service account the needed permissions.

In Kubernetes you grant the needed permissions by creating a `ClusterRoleBinding` that refers to "system:persistent-volume-provisioner".
In OpenShift you do so by running something like: `oadm policy add-cluster-role-to-user system:persistent-volume-provisioner system:serviceaccount:default:my-provisioner`

For an example of what all this looks like, see the [EFS provisioner documentation](https://github.com/kubernetes-incubator/external-storage/tree/master/aws/efs#authorization) and its associated [yamls](https://github.com/kubernetes-incubator/external-storage/tree/master/aws/efs/deploy/auth).

## Running multiple provisioners and giving provisioners identities

You must determine whether you want to support the use-case of running multiple provisioner-controller instances in a cluster. Further, you must determine whether you want to implement this identity idea to address that use-case.

The library supports running multiple instances out of the box via its basic leader election implementation wherein multiple controllers trying to provision for the same class of claims race to lock/lead claims in order to be the one to provision for them. This prevents multiple provisioners from needlessly calling `Provision`, which is undesirable because only one will succeed in creating a PV and the rest will have wasted API calls and/or resources creating useless storage assets. Configuration of all this is done via controller parameters.

There is no such race to lock implementation for deleting PVs: all provisioners will call `Delete`, repeatedly until the storage asset backing the PV and the PV are deleted. This is why it's desirable to implement the identity idea, so that only the provisioner who is *responsible* for deleting a PV actually attempts to delete the PV's backing storage asset. The rest should return the special `IgnoredError` which indicates to the controller that they ignored the PV, as opposed to trying and failing (which would result in a misleading error message) or succeeding (obviously a bad idea to lie about that).

In some cases, the provisioner who is *responsible* for deleting a PV is also the only one *capable* of deleting a PV, in which case it's not only desirable to implement the identity idea, but necessary. This is the case with the `hostPath` provisioner example: obviously only the provisioner running on a certain host can delete the backing storage asset because the backing storage asset is local to the host.

Now, actually giving provisioners identities and effectively making them pets may be the hard part. In the `hostPath` example, the sensible thing to do was tie a provisioner's identity to the node/host it runs on. In your case, maybe it makes sense to tie each provisioner to e.g. a certain member in a storage pool. And should a certain provisioner die, when it comes back it should retain its identity lest the cluster be left with dangling volumes that no running provisioner can delete.

## Contributing

This repository is structured such that each external provisioner gets its own directory for its code, docs, examples, yamls, etc. What they don't get is individual "vendor" directories for their respective dependencies, they must depend on the shared top-level vendor and lib directories. This helps reduce the size of the repo and forces all parts of it to stay updated, but introduces some complications for contributors.

### Conventions
[Kubernetes project](https://github.com/kubernetes/kubernetes/) conventions are followed if not otherwise stated.

### Adding a provisioner

Basically you create a directory to house everything you want to check in, add build and/or test invocations to [travis](../.travis.yml), and add dependencies to the top-level vendor directory.

### Adding a vendor dependency

This repository uses [glide](https://github.com/Masterminds/glide) for package management. Add the packages to [glide.yaml](../glide.yaml), run "glide up -v", then run "glide-vc --use-lock-file".

### Updating a vendor dependency and/or contributing to the library

Any breaking update to a vendor dependency requires an update to every external provisioner that depends on it. It follows that any breaking update to the library requires an update to every external provisioner. If the provisioners that need to be updated are not updated, they simply won't build.

Generally, breaking vendor dependency updates won't happen often (at least every time kubernetes/client-go updates, maybe) and all the provisioners can be updated with ease, without requiring explicit approval from their respective OWNERS, unless the change is big enough or they've asked that it be required.

As the contributor of a dependency/library update, you're usually responsible for updating the dependents so travis CI passes, as it shouldn't be harder than a find/replace. Otherwise, if it's decided that you don't need to be responsible, some other solution will be worked out to make sure everything stays in a buildable state.

### Using Persistent Volume annotations
External provisioners may need to store custom data in Persistent Volume annotations. An annotation should have the below format:
```
annotations:
  <provisioner-type>.external-storage.incubator.kubernetes.io/<variable> : <value>
```
A usage example:
```
annotations:
  "manila.external-storage.incubator.kubernetes.io/ID": "de64eb77-05cb-4502-a6e5-7e8552c352f3"
```
