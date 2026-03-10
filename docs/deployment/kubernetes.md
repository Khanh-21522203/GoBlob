# Kubernetes Deployment

```bash
kubectl apply -f k8s/master-statefulset.yaml
kubectl apply -f k8s/volume-statefulset.yaml
kubectl apply -f k8s/filer-deployment.yaml
kubectl apply -f k8s/s3-deployment.yaml
```

Use StatefulSets for master/volume so storage identity stays stable.
