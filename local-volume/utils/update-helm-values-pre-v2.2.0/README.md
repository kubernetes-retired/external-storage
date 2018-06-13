## update helm values pre v2.2.0

If you are using old helm chart (pre local-volume-provisioner-v2.2.0), you can
use this utility to upgrade your values file:

```
go run utils/update-helm-values-pre-v2.2.0/main.go -input <your-old-values-file> -engine <engine>
```
