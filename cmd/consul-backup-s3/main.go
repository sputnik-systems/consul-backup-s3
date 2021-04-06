package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/snapshot"
	"github.com/rboyer/safeio"
)

var (
	cfg      *api.Config
	log      *logrus.Logger
	ttl      time.Duration
	endpoint string
	region   string
	bucket   string
	prefix   string
)

func init() {
	var err error

	cfg = api.DefaultConfig()
	log = logrus.New()

	// consul connect settings
	flag.StringVar(&cfg.Address, "consul.address", "http://127.0.0.1:8500", "consul server http address")
	flag.StringVar(&cfg.Token, "consul.token", "", "consul server token")
	flag.StringVar(&cfg.TokenFile, "consul.token-file", "", "consul server token file")
	flag.StringVar(&cfg.Datacenter, "consul.datacenter", "", "consul datacenter name")
	flag.StringVar(&cfg.TLSConfig.CAFile, "consul.ca-file", "", "consul server CA file")
	flag.StringVar(&cfg.TLSConfig.CAPath, "consul.ca-path", "", "consul server CA path")
	flag.StringVar(&cfg.TLSConfig.CertFile, "consul.client-cert", "", "consul client cert file")
	flag.StringVar(&cfg.TLSConfig.KeyFile, "consul.client-key", "", "consul client key file")
	flag.StringVar(&cfg.TLSConfig.Address, "consul.tls-server-name", "", "consul server name for tls communication")
	// s3 connect settings
	flag.StringVar(&endpoint, "s3.endpoint", "", "s3 endpoint url")
	flag.StringVar(&region, "s3.region", "", "s3 bucket region")
	flag.StringVar(&bucket, "s3.bucket", "", "s3 bucket name for upload (required)")
	flag.StringVar(&prefix, "s3.prefix", "", "s3 backup store location prefix")
	// backup settings
	schedule := flag.String("backup.schedule", "0 0 * * *", "crontab format schedule for backup create")
	duration := flag.String("backup.ttl", "744h0m0s", "golang duration format ttl")
	flag.Parse()

	if bucket == "" {
		log.Fatal("missing bucket parameter")
	}

	ttl, err = time.ParseDuration(*duration)
	if err != nil {
		log.Fatalf("failed parsing backup ttl: %s", err)
	}

	cron := cron.New(
		cron.WithLogger(cron.VerbosePrintfLogger(log)),
	)
	cron.AddFunc(*schedule, makeBackup)
	cron.AddFunc(*schedule, rotateBackups)
	cron.Start()

	rotateBackups()
}

func main() {
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func makeBackup() {
	snap, err := GetSnapshot(cfg)
	if err != nil {
		log.Errorf("failed to get snapshot: %s", err)
	}

	now := time.Now()
	name := fmt.Sprintf("consul_%d.snap", now.Unix())

	sess := session.Must(
		session.NewSession(&aws.Config{
			Endpoint: aws.String(endpoint),
			Region:   aws.String(region),
		}))
	uploader := s3manager.NewUploader(sess)

	body := bytes.NewReader(snap)
	key := path.Join(prefix, name)
	_, err = uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   body,
	})
	if err != nil {
		log.Errorf("failed to upload file, %v", err)
		return
	}

	log.Infof("snapshot %s successfully uploaded to s3://%s", name, path.Join(bucket, prefix))
}

func rotateBackups() {
	sess := session.Must(
		session.NewSession(&aws.Config{
			Endpoint: aws.String(endpoint),
			Region:   aws.String(region),
		}))

	svc := s3.New(sess)
	input := &s3.ListObjectsInput{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	}

	resp, err := svc.ListObjects(input)
	if err != nil {
		log.Errorf("failed to list snapshots in s3 bucket: %s", err)
		return
	}

	var ids []*s3.ObjectIdentifier
	var snaps []string // store snapshots for log message generation

	r := regexp.MustCompile(`consul_(?P<ts>\d+).snap$`)
	now := time.Now()
	for _, obj := range resp.Contents {
		key := *obj.Key
		match := r.FindStringSubmatch(key)
		if len(match) >= 2 {
			t, err := strconv.ParseInt(match[1], 10, 64)
			if err != nil {
				log.Errorf("falied to parse timestamp of snapshot %s: %s", key, err)
				return
			}

			ts := time.Unix(t, 0)
			if ts.Add(ttl).Unix() < now.Unix() {
				snaps = append(snaps, key) // only for log message
				ids = append(ids, &s3.ObjectIdentifier{Key: &key})
			}
		}
	}

	if len(ids) > 0 {
		input := &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3.Delete{
				Objects: ids,
				Quiet:   aws.Bool(false),
			},
		}

		_, err := svc.DeleteObjects(input)
		if err != nil {
			log.Errorf("failed to delete objects from s3: %s", err)
			return
		}
	}

	log.Infof("removed snapshots: %v", snaps)
}

func GetSnapshot(cfg *api.Config) ([]byte, error) {
	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed initialize consul client: %s", err)
	}

	snap, qm, err := client.Snapshot().Save(&api.QueryOptions{
		AllowStale: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed create consul snapshot: %s", err)
	}
	defer snap.Close()

	// Save the file for snapshot verification.
	unverifiedFile := "snap" + ".unverified"
	if _, err := safeio.WriteToFile(snap, unverifiedFile, 0666); err != nil {
		return nil, fmt.Errorf("error writing unverified snapshot file: %s", err)
	}
	defer os.Remove(unverifiedFile)

	// Read it back to verify.
	f, err := os.Open(unverifiedFile)
	if err != nil {
		return nil, fmt.Errorf("error opening snapshot file for verify: %s", err)
	}
	defer f.Close()

	body, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot body: %s", err)
	}

	r := bytes.NewReader(body)
	if _, err := snapshot.Verify(r); err != nil {
		return nil, fmt.Errorf("error verifying snapshot file: %s", err)
	}

	log.Infof("saved and verified snapshot to index %d", qm.LastIndex)

	return body, nil
}
