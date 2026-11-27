# pod2daemon

pod2daemon enables secure communication between a Kubernetes pod and a daemon
running on the host (for example, one created by a DaemonSet). It gives each
workload its own Unix domain socket directory and records the workload's
identity, so that the daemon can tell which pod is on the other end.

## What is in this directory

- `csidriver/`, `pkg/csi/`: the `csi.tigera.io` CSI driver. When a pod mounts a
  volume of this type, the driver creates the pod's socket directory under
  `/var/run/nodeagent`, mounts it into the pod, and writes the pod's
  credentials for the daemon to read.
- `binder/`: the daemon side. It watches the credentials and serves gRPC on
  each workload's socket, attaching the workload's identity to every call.
- `workloadapi/`, `nodeagent/`: an example service and daemon built on the
  binder.
- `pkg/flexvol/`: removes the `nodeagent/uds` FlexVolume driver that earlier
  releases installed on the host. That driver is no longer shipped.

## How to set up a workload

```yaml
...snip
      containers:
        volumeMounts:
        - mountPath: /var/run/policysync
          name: policysync
      volumes:
        - name: policysync
          csi:
            driver: csi.tigera.io
```

## Migrating from the FlexVolume driver

Replace any `flexVolume` volume that uses `driver: nodeagent/uds` with the
`csi` volume shown above. An installed FlexVolume driver is removed from a node
the next time calico-node starts with no pod on that node still mounting it.
