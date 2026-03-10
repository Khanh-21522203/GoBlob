# Configuration

Core runtime flags:
- `blob master`: `-port`, `-grpc.port`, `-mdir`, `-metricsPort`
- `blob volume`: `-port`, `-grpc.port`, `-masters`, `-dir`, `-metricsPort`
- `blob filer`: `-port`, `-grpc.port`, `-masters`, `-storeDir`, `-storeBackend`, `-metricsPort`
- `blob s3`: `-port`, `-filer`, `-metricsPort`

Security and reload support are controlled by existing security configuration loading in command runtime (SIGHUP reload).
