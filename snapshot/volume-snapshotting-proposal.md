Kubernetes Snapshotting Proposal
================================

**Authors:** [Cindy Wang](https://github.com/ciwang), [Jing Xu](https://github.com/jinxu97),[Tomas Smetana](https://github.com/tsmetana), [Huamin Chen ](https://github.com/rootfs)

## Background

Many storage systems (GCE PD, Amazon EBS, etc.) provide the ability to create "snapshots" of a persistent volumes to protect against data loss. Snapshots can be used in place of a traditional backup system to back up and restore primary and critical data. Snapshots allow for quick data backup (for example, it takes a fraction of a second to create a GCE PD snapshot) and offer fast recovery time objectives (RTOs) and recovery point objectives (RPOs).

Typical existing backup solutions offer on demand or scheduled snapshots.

An application developer using a storage may want to create a snapshot before an update or other major event. Kubernetes does not currently offer a standardized snapshot API for creating, listing, deleting, and restoring snapshots on an arbitrary volume.

Existing solutions for scheduled snapshotting include [cron jobs](https://forums.aws.amazon.com/message.jspa?messageID=570265) and [external storage drivers](http://rancher.com/introducing-convoy-a-docker-volume-driver-for-backup-and-recovery-of-persistent-data/). Some cloud storage volumes can be configured to take automatic snapshots, but this is specified on the volumes themselves.

## Objectives

For the first version of snapshotting support in Kubernetes, only on-demand snapshots will be supported. Features listed in the roadmap for future versions are also nongoals.
The initial version is created as an external controller and provisioner (i.e. two separate binaries).

* Goal 1: Enable *on-demand* snapshots of Kubernetes persistent volumes by application developers.

    * Nongoal: Enable *automatic* periodic snapshotting for direct volumes in pods.

* Goal 2: Expose standardized snapshotting operations to create, list and delete snapshots in Kubernetes REST API.

* Goal 3: Implement snapshotting interface for Amazon EBS.

* Goal 4: Implement snapshotting interface for GCE PDs.

### Feature Roadmap

Major features, planned for the first version:

* On demand snapshots

    * API to create new snapshots

    * API to list snapshots available to the user

    * API to delete existing snapshots

    * API to create a new persistent volume with persistent volume claim from a snapshot

### Future Features

The features that are not planned for the first version of the API bus should be considered in future versions:

* Creating snapshots

    * Scheduled and periodic snapshots

    * Application initiated on-demand snapshot creation

    * Support snapshot per PVC, pod or StatefulSet

    * Support snapshots for non-cloud storage volumes (plugins might require actions to be triggered from the node)

    * Support application-consistent snapshots (coordinate distributed snapshots across multiple volumes)

    * Enable to create a pod/statefulsets with snapshots

* List snapshots

    * Enable to get the list of all snapshots for a specified persistent volume

    * Enable to get the list of all snapshots for a pod/StatefulSet

* Delete snapshots

    * Enable to automatic garbage collect older snapshots when storage is limited

* Quota management

    * Enable to set quota for limiting how many snapshots could be taken and saved

    * When quota is exceeded, delete the oldest snapshots automatically

## Requirements

### Performance

* Time SLA from issuing a snapshot to completion:

* The period we are interested is the time between the scheduled snapshot time, the time the snapshot is cut (it is safe to write to the snapshotted volume again) and the time the snapshot is finished uploading to its storage location.

* This should be on the order of a few minutes.

### Reliability

* Data corruption

    * Though it is generally recommended to stop application writes before executing the snapshot command, we will not do this automatically for several reasons:

        * GCE and Amazon can create snapshots while the application is running.

        * Stopping application writes cannot be done from the master and varies by application, so doing so will introduce unnecessary complexity and permission issues in the code.

        * Most file systems and server applications are (and should be) able to restore inconsistent snapshots the same way as a disk that underwent an unclean shutdown.

    * The data consistency would be best-effort only: e.g., call fsfreeze prior the snapshot on filesystems that support it.

    * There are several proposed solutions that would enable the users to specify the action to perform prior to/after the
    snapshots: e.g. use pod annotations.

* Snapshot failure

    * Case: Failure during external process, such as during API call or upload

        * Log error, do not attempt to retry

    * Case: Failure within Kubernetes, such as controller restarts

        *  If the master restarts in the middle of a snapshot operation, then the controller will find a snapshot request in pending state and should be able to successfully finish the operation.

## Solution Overview

There are a few uniqueness related to snapshots:

* Both users and admins might create snapshots. Users should only get access to the snapshots belonging to their namespaces. For this aspect, snapshot objects should be in user namespace. Admins might want to choose to expose the snapshots they created to some users who have access to those volumes.

* After snapshots are taken, users might use them to create new volumes or restore the existing volumes back to the time when the snapshot is taken.

* There are use cases that data from snapshots taken from one namespace need to be accessible by users in another namespace.

* For security purpose, if a snapshot object is created by a user, kubernetes should prevent other users duplicating this object in a different namespace if they happen to get the snapshot name.

* There might be some existing snapshots taken by admins/users and they want to use those snapshots through kubernetes API interface.

* **Create:**

    1. The user creates a `VolumeSnapshot` referencing a persistent volume claim bound to a persistent volume

    2. The controller fulfils the `VolumeSnapshot` by creating a snapshot using the volume plugins.

    3. A new object `VolumeSnapshotData` is created to represent the actual snapshot binding the `VolumeSnapshot` with
       the on-disk snapshot.

* **List:**

    1. The user is able to list all the `VolumeSnapshot` objects in the namespace

* **Delete:**

    1. The user deletes the `VolumeSnapshot`

    2. The controller removes the on-disk snapshot. Note: snapshots have no notion of "reclaim policy" - there is
       no way to recover the deleted snapshot.

    3. The controller removes the `VolumeSnapshotData` object.

* **Promote snapshot to PV:**

    1. The user creates a persistent volume claim referencing the snapshot object in the annotation. The PVC must
       belong to a `StorageClass` using the external volume snapshot provisioner. Note: the special annotation might get replaced
       by a dedicated attribute of the `PersistentVolumeClaim` in the future.

    2. The provisioner will use the `VolumeSnapshotData` object to create a persistent volume using the corresponding
       volume snapshot plugin.

    3. The PVC is bound to the newly created PV containing the data from the snapshot.


The snapshot operation is a no-op for volume plugins that do not support snapshots via an API call (i.e. non-cloud storage).


### API

* The `VolumeSnapshot` object

```
// The volume snapshot object accessible to the user. Upon succesful creation of the actual
// snapshot by the volume provider it is bound to the corresponding VolumeSnapshotData through
// the VolumeSnapshotSpec
type VolumeSnapshot struct {
	metav1.TypeMeta `json:",inline"`
	Metadata        metav1.ObjectMeta `json:"metadata"`

	// Spec represents the desired state of the snapshot
	// +optional
	Spec VolumeSnapshotSpec `json:"spec" protobuf:"bytes,2,opt,name=spec"`

	// Status represents the latest observer state of the snapshot
	// +optional
	Status VolumeSnapshotStatus `json:"status" protobuf:"bytes,3,opt,name=status"`
}

type VolumeSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	Metadata        metav1.ListMeta  `json:"metadata"`
	Items           []VolumeSnapshot `json:"items"`
}

// The description of the volume snapshot
type VolumeSnapshotSpec struct {
	// Source represents the source of the volume snapshot
	VolumeSnapshotSource `json:",inline" protobuf:"bytes,1,opt,name=volumeSnapshotSource"`
	// VolumeSnapshotDataName binds the VolumeSnapshot object with the VolumeSnapshotData
	// +optional
	VolumeSnapshotDataName string `json:"volumeSnapshotDataName" protobuf:"bytes,2,opt,name=volumeSnapshotDataName"`
}

// The source of the volume snapshot
type VolumeSnapshotSource struct {
	// PersistentVolumeClaimName is the name of the PVC being snapshotted
	// +optional
	PersistentVolumeClaimName string `json:"persistentVolumeClaimName" protobuf:"bytes,1,opt,name=persistentVolumeClaimName"`

	// ... other source types might be added in the future
}

type VolumeSnapshotStatus struct {
	// The time the snapshot was successfully created
	// +optional
	CreationTimestamp metav1.Time `json:"creationTimestamp" protobuf:"bytes,1,opt,name=creationTimestamp"`

	// Representes the lates available observations about the volume snapshot
	Conditions []VolumeSnapshotCondition `json:"conditions" protobuf:"bytes,2,rep,name=conditions"`
}

type VolumeSnapshotConditionType string

// These are valid conditions of a volume snapshot.
const (
	// VolumeSnapshotConditionCreated is present and set to ConditionTrue when the snapshot has been successfully created
	// The snapshot might not be ready to use yet: there are still some pending operations that don't need the
	// original volume being snapshotted (eg. the snapshot data need to be uploaded to a dedicated storage)
	VolumeSnapshotConditionCreated VolumeSnapshotConditionType = "Created"
	// VolumeSnapshotConditionReady is present and set to ConditionTrue when the snapshot is ready to be used
	VolumeSnapshotConditionReady VolumeSnapshotConditionType = "Ready"
)

// VolumeSnapshot Condition describes the state of a volume snapshot at a certain point.
type VolumeSnapshotCondition struct {
	// Type of replication controller condition.
	Type VolumeSnapshotConditionType `json:"type" protobuf:"bytes,1,opt,name=type,casttype=VolumeSnapshotConditionType"`
	// Status of the condition, one of True, False, Unknown.
	Status core_v1.ConditionStatus `json:"status" protobuf:"bytes,2,opt,name=status,casttype=ConditionStatus"`
	// The last time the condition transitioned from one status to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime" protobuf:"bytes,3,opt,name=lastTransitionTime"`
	// The reason for the condition's last transition.
	// +optional
	Reason string `json:"reason" protobuf:"bytes,4,opt,name=reason"`
	// A human readable message indicating details about the transition.
	// +optional
	Message string `json:"message" protobuf:"bytes,5,opt,name=message"`
}
```

* The `VolumeSnapshotData` object

```
// +genclient=true
// +nonNamespaced=true

// VolumeSnapshotData represents the actual "on-disk" snapshot object
type VolumeSnapshotData struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	Metadata metav1.ObjectMeta `json:"metadata"`

	// Spec represents the desired state of the snapshot
	// +optional
	Spec VolumeSnapshotDataSpec `json:"spec" protobuf:"bytes,2,opt,name=spec"`

	// Status represents the latest observed state of the snapshot
	// +optional
	Status VolumeSnapshotDataStatus `json:"status" protobuf:"bytes,3,opt,name=status"`
}

// The desired state of the volume snapshot
type VolumeSnapshotDataSpec struct {
	// Source represents the location and type of the volume snapshot
	VolumeSnapshotDataSource `json:",inline" protobuf:"bytes,1,opt,name=volumeSnapshotDataSource"`

	// VolumeSnapshotRef is part of bi-directional binding between VolumeSnapshot
	// and VolumeSnapshotData
	// +optional
	VolumeSnapshotRef *core_v1.ObjectReference `json:"volumeSnapshotRef" protobuf:"bytes,2,opt,name=volumeSnapshotRef"`

	// PersistentVolumeRef represents the PersistentVolume that the snapshot has been
	// taken from
	// +optional
	PersistentVolumeRef *core_v1.ObjectReference `json:"persistentVolumeRef" protobuf:"bytes,3,opt,name=persistentVolumeRef"`
}

// Represents the actual location and type of the snapshot. Only one of its members may be specified.
type VolumeSnapshotDataSource struct {
	// HostPath represents a directory on the host.
	// Provisioned by a developer or tester.
	// This is useful for single-node development and testing only!
	// On-host storage is not supported in any way and WILL NOT WORK in a multi-node cluster.
	// More info: https://kubernetes.io/docs/concepts/storage/volumes#hostpath
	// +optional
	HostPath *HostPathVolumeSnapshotSource `json:"hostPath,omitempty"`
	// AWSElasticBlockStore represents an AWS Disk resource that is attached to a
	// kubelet's host machine and then exposed to the pod.
	// More info: https://kubernetes.io/docs/concepts/storage/volumes#awselasticblockstore
	// +optional
	AWSElasticBlockStore *AWSElasticBlockStoreVolumeSnapshotSource `json:"awsElasticBlockStore,omitempty"`

    // ... etc. for other snapshot types
}
```

An example of the `VolumeSnapshotDataSource` for Amazon EBS snapshots:

```
// AWS EBS volume snapshot source
type AWSElasticBlockStoreVolumeSnapshotSource struct {
	// Unique id of the persistent disk snapshot resource. Used to identify the disk snapshot in AWS
	SnapshotID string `json:"snapshotId"`
}
```

### Event loop in controller

The volume snapshot controller maintains two data structures (ActualStateOfWorld and DesiredStateOfWorld) and
periodically reconciles the two. The data structures are being update by the API sever event handlers.

* If a new `VolumeSnapshot` is added, the *add* handler adds it to the DesiredStateOfWorld (DSW)

* If a `VolumeSnapshot` is deleted, the *delete* handler removes it from the DSW.

* Reconciliation loop in the controller

    * For every `VolumeSnapshot` in the ActualStateOfWorld (ASW) find the corresponding `VolumeSnapshot` in the DSW.
    If such a snapshot does not exist, start a snapshot deletion operation:

        * Determine the correct volume snapshot plugin to use from the `VolumeSnapshotData` referenced by the
        `VolumeSnapshot`

        * Create a delete operation: only one such operation is allowed to exist for the given `VolumeSnapshot` and
        `VolumeSnapshotData` pair

        * The operation is an asynchronous function using the volume plugin to delete the actual snapshot in the back-end.

        * When the plugin finishes deleting the snapshot, delete the `VolumeSnapshotData` referencing it and remove the
        `VolumeSnapshot` reference from the ASW

    * For every `VolumeSnapshot` in the DSW find the corresponding `VolumeSnapshot` in the ASW. If such a snapshot
    does not exist, start a snapshot creation operation:

        * Determine the correct volume snapshot plugin to use from the `VolumeSnapshotData` referenced by the
        `VolumeSnapshot`

        * Create a volume snapshot creation operation: only one such operation is allowed to exist for the given
        `VolumeSnapshot` and `VolumeSnapshotData` pair.

        * The operation is an asynchronous function using the volume plugin to create the actual snapshot in the back-end.

        * When the plugin finishes creating the snapshot a new `VolumeSnapshotData` is created holding a reference to
        the actual volume snapshot.

    * For every snapshot present in the ASW and DSW find its `VolumeSnapshotData` and verify the bi-directional
    binding is correct: if not, update the `VolumeSnapshotData` reference.


### Create Snapshot Logic

To create a snapshot:

* Acquire operation lock for volume so that only one one snapshot creation operation is running for the specified volume

    * Abort if there is already a pending operation (reconciliation loop will retry, if needed).

* Spawn a new thread:

    * Execute the volume-specific logic to create a snapshot of the persistent volume referenced by the PVC.

    * For any errors, log the error, send it as an event on the corresponding `VolumeSnapshot`, and terminate the thread (the main controller will retry as needed).

    * Once a snapshot is created successfully:

        * Make a call to the API server to add the new snapshot ID/timestamp to the `VolumeSnapshotData` API object, update its
        status.

### Snapshot to PV promotion logic

For the `PersistentVolumeClaim` provisioned by the snapshot external provisioner:

* Check the `PeristentVolumeClaim` annotation and get the `VolumeSnapshot` name.
  
* Retrieve the `VolumeSnapshot` object from the API server:
  
    * Verify both the `VolumeSnapshot` and `PersistentVolumeClaim` belong to the same namespace.
    
    * Verify the `VolumeSnapshotData` referenced by the `VolumeSnapshot` exists and that its `VolumeSnapshot` reference points "back" to the given `VolumeSnapshot` object.
    
* Find the correct plugin to use to create a `PersistentVolume` bound to the given PVC.

## Example Use Case

### Alice wants to backup her MySQL database data

Alice is a DB admin who runs a MySQL database and needs to backup the data on a remote server prior to the database
upgrade. She has a short maintenance window dedicated to the operation that allows her to pause the dabase only for
a short while. Alice will therefore stop the database, create a snapshot of the data, re-start the database and after
that start time-consuming network transfer to the backup server.

The database is running in a pod with the data stored on a persistent volume:
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: mysql
  labels:
    name: mysql
spec:
  containers:
    - resources:
        limits :
          cpu: 0.5
      image: openshift/mysql-55-centos7
      name: mysql
      env:
        - name: MYSQL_ROOT_PASSWORD
          value: rootpassword
        - name: MYSQL_USER
          value: wp_user
        - name: MYSQL_PASSWORD
          value: wp_pass
        - name: MYSQL_DATABASE
          value: wp_db
      ports:
        - containerPort: 3306
          name: mysql
      volumeMounts:
        - name: mysql-persistent-storage
          mountPath: /var/lib/mysql/data
  volumes:
    - name: mysql-persistent-storage
      persistentVolumeClaim:
      claimName: claim-mysql
```

The persistent volume is bound to the `claim-mysql` PVC which needs to be snapshotted. Since Alice has some downtime
allowed she may lock the database tables for a moment to ensure the backup would be consistent:
```
mysql> FLUSH TABLES WITH READ LOCK;
```
Now she is ready to create a snapshot of the `claim-mysql` PVC. She creates a vs.yaml:
```yaml
apiVersion: v1
kind: VolumeSnapshot
metadata:
  name: mysql-snapshot
  namespace: default
spec:
  persistentVolumeClaim: claim-mysql
```

```
$ kubectl create -f vs.yaml
```

This will result in a new snapshot being created by the controller. Alice would wait until the snapshot is complete:
```
$ kubectl get volumesnapshots

NAME             STATUS
mysql-snapshot   ready
```
Now it's OK to unlock the database tables and the database may return to normal operation:
```
mysql> UNLOCK TABLES;
```
Alice can now get to the snapshotted data and start syncing them to the remote server. First she needs to promote the
snapshot to a PV by creating a new PVC. To use the external provisioner a new storage class must be created:
```yaml
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: snapshot-promoter
provisioner: volumesnapshot.external-storage.k8s.io/snapshot-promoter
```
Now Alice can create the PVC referencing the snapshot in the annotations.
```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: snapshot-data-claim
annotations:
    snapshot.alpha.kubernetes.io/snapshot: mysql-snapshot
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: snapshot-promoter
```
Once the claim is bound to a persistent volume Alice creates a job to sync the data with a remote backup server:
```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: mysql-sync
spec:
  template:
    metadata:
      name: mysql-sync
    spec:
      containers:
      - name: mysql-sync
        image: rsync
        command: "rsync -av /mnt/data alice@backup.example.com:mysql_backups"
      restartPolicy: Never
      volumeMounts:
        - name: snapshot-data
          mountPath: /mnt/data
  volumes:
    - name: snapshot-data
      persistentVolumeClaim:
      claimName: snapshot-data-claim
```

Alice will wait for the job to finish and then may delete both the `snapshot-data-claim` PVC as well as `mysql-snapshot`
request (which will delete also the snapshot object):
```
$ kubectl delete pvc snapshot-data-claim
$ kubectl delete volumesnapshot mysql-snapshot
```
