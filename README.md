# Usage
You can look at help message to get available options:
```
$ go run cmd/consul-backup-s3/main.go --help                                                                                                
Usage of /tmp/go-build3397545109/b001/exe/main:                                                                             
      --backup.schedule string          crontab format schedule for backup create (default "0 0 * * *")
      --backup.ttl string               golang duration format ttl (default "744h0m0s")
      --consul.address string           consul server http address (default "http://127.0.0.1:8500")
      --consul.ca-file string           consul server CA file
      --consul.ca-path string           consul server CA path
      --consul.client-cert string       consul client cert file
      --consul.client-key string        consul client key file
      --consul.datacenter string        consul datacenter name
      --consul.tls-server-name string   consul server name for tls communication
      --consul.token string             consul server token
      --consul.token-file string        consul server token file
      --s3.bucket string                s3 bucket name for upload (required)
      --s3.endpoint string              s3 endpoint url
      --s3.prefix string                s3 backup store location prefix
      --s3.region string                s3 bucket region
pflag: help requested
exit status 2
```
## S3 options
For not amazon cloud provider, you need specify endpoint and region.
## Schedule option format
Schedule option support cron like notation. Also supported values like "@hourly", "@daily", "@weekly", "@monthly", "@yearly", "@annually" and "@every duraton"(duration must be parsable with time.ParseDuration).
