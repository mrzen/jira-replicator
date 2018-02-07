package main

import (
	"github.com/urfave/cli"
	"github.com/spf13/viper"
	"os"
	"fmt"
	"net/url"
	"github.com/mrzen/jira-replicator/client"
	"errors"
	"io"
	"github.com/cheggaaa/pb"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"time"
	"bytes"
	"log"
	"math"
)

const partSize = 64 * 1<<20

func main() {

	app := cli.NewApp()
	app.Name = "JIRA Replicator"

	app.Before = setup


	app.Commands = []cli.Command{
		{
			Name: "backup",
			Aliases: []string{"b"},
			Description: "Create a new backup",
			Action: makeBackup,
		},

		{
			Name: "download",
			Aliases: []string{"d"},
			Description: "Download backup file",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name: "o",
					Usage: "Output file location",

				},
			},
			Action: downloadBackup,
		},

		{
			Name: "s3",
			Description: "Upload backup to S3",
			Action: backupToS3,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name: "config",
					Usage: "Configuration file path",
				},
			},
		},

		{
			Name: "daemon",
			Description: "Replication daemon, creates backups and copies to S3 as often as possible.",
			Action: replicate,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name: "config",
					Usage: "Configuration file path",
				},
			},
		},
	}

	app.Run(os.Args)

}

func setup(ctx *cli.Context) error {
	viper.SetEnvPrefix("jira")

	// TODO: Change this to also work with EC2 Parameter Store (+KMS)
	viper.BindEnv("url")
	viper.BindEnv("username")
	viper.BindEnv("password")

	u, err := url.Parse(viper.GetString("url"))

	if err != nil {
		fmt.Fprintln(ctx.App.Writer, "Unable to parse JIRA URL: ", err)
		return err
	}

	viper.Set("url", u)

	return nil

}


func replicate(ctx *cli.Context) error {
	c := client.New(viper.Get("url").(*url.URL), viper.GetString("username"), viper.GetString("password"))

	for {
		for {
			err := makeBackup(ctx)

			if err != nil {
				switch v := err.(type) {
				case client.BackupRateExceeded:
					fmt.Fprintln(ctx.App.Writer, "Backup rate exceeded. Retrying in", v.RetryIn())
					time.Sleep(v.RetryIn())
				default:
					fmt.Fprintln(ctx.App.Writer, "Couldn't make a backup.")
					return err
				}
			}

			break
		}

		start := time.Now()
		fmt.Fprintln(ctx.App.Writer, "Waiting for backup to be ready")

		waiter := time.NewTicker(30*time.Second)

		for {
			<- waiter.C

			fmt.Fprintln(ctx.App.Writer,"Checking backup status")
			status, err := c.GetBackupStatus()


			if err != nil {
				fmt.Fprintln(ctx.App.Writer, "Unable to get backup status: ", err)
			}

			if status.Status != "InProgress" {
				break
			}
		}


		fmt.Fprintln(ctx.App.Writer,"Copying backup to S3")
		err := backupToS3(ctx)
		duration := time.Now().Sub(start)

		if err != nil {
			fmt.Fprintln(ctx.App.Writer, "Unable to copy backup to S3")
		} else {
			fmt.Fprintln(ctx.App.Writer, "Backup and copy to S3 completed. Took", duration)
			fmt.Fprintln(ctx.App.Writer, "Starting a new backup in 48 hours at:", time.Now().Add(48*time.Hour).Format(time.RFC822Z))
			time.Sleep(48*time.Hour)
		}
	}
}

func downloadBackup(ctx *cli.Context) error {
	output := ctx.String("o")
	if output == "" {
		fmt.Fprintln(ctx.App.Writer, "Output file path is required")
		return errors.New("output file path is required")
	}

	file, err := os.OpenFile(output, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)

	if err != nil {
		fmt.Fprintln(ctx.App.Writer, "Unable to open download file: ", err)
		return err
	}

	c := client.New(viper.Get("url").(*url.URL), viper.GetString("username"), viper.GetString("password"))

	reader, length, err := c.DownloadBackup()

	if err != nil {
		fmt.Fprintln(ctx.App.Writer, "Unable to get backup: ", err)
		return err
	}

	// Create a Tee reader to do some
	bar := pb.New(length).SetUnits(pb.U_BYTES)
	bar.Start()

	br := bar.NewProxyReader(reader)
	io.Copy(file, br)
	bar.Finish()

	return err

}

func makeBackup(ctx *cli.Context) error {
	c := client.New(viper.Get("url").(*url.URL), viper.GetString("username"), viper.GetString("password"))
	err := c.CreateBackup(true)

	if err != nil {
		switch v := err.(type) {
		case client.BackupRateExceeded:
			fmt.Fprintln(ctx.App.Writer, "Backup rate exceeded. Try again at:", v.RetryAt())
		default:
			fmt.Fprintln(ctx.App.Writer, "Unable to create backup:", err)
		}

	}

	return err
}

func backupToS3(ctx *cli.Context) error {

	l := log.New(ctx.App.Writer, "s3-backup ", log.LstdFlags)

	viper.SetConfigFile(ctx.String("config"))
	err := viper.ReadInConfig()

	if err != nil {
		l.Println("Unable to read configuration file.")
		return nil
	}

	c := client.New(viper.Get("url").(*url.URL), viper.GetString("username"), viper.GetString("password"))

	reader, size, err := c.DownloadBackup()

	if err != nil {
		l.Println("Unable to get backup: ", err)
		return nil
	}

	awsSession := session.Must(session.NewSession(&aws.Config{
		Region: aws.String(viper.GetString("s3.region")),
	}))

	s3Client := s3.New(awsSession)

	bucketName := viper.GetString("s3.bucket")
	keyName := fmt.Sprintf("jira-%s.zip", time.Now().Format("2006-01-02"))

	uploadReq := &s3.CreateMultipartUploadInput{
		Bucket: &bucketName,
		Key: &keyName,
		ContentType: aws.String("application/zip"),
		ACL: aws.String(s3.BucketCannedACLPrivate),
	}

	if storageClass := viper.GetString("s3.storage_class"); storageClass != "" {
		uploadReq.StorageClass = &storageClass
	}

	if kmsKeyId := viper.GetString("kms.key"); kmsKeyId != "" {
		uploadReq.ServerSideEncryption = aws.String(s3.ServerSideEncryptionAwsKms)
		uploadReq.SSEKMSKeyId = &kmsKeyId
	}

	upload, err := s3Client.CreateMultipartUpload(uploadReq)

	if err != nil {
		l.Println("Unable to create multi-part upload:", err)
		return err
	}

	i := 0

	var partMap []*s3.CompletedPart

	bar := pb.New(size).SetUnits(pb.U_BYTES)

	barReader := bar.NewProxyReader(reader)

	bar.Start()

	nParts := int(math.Ceil(float64(size) / float64(partSize)))



	for partNumber := int64(1); i < size; partNumber++ {
		bar.Postfix( fmt.Sprintf(" %02d/%02d", partNumber, nParts) )
		// Read a part's worth of data from JIRA
		store := make([]byte, partSize) // 16MiB

		bytesRead, err := io.ReadFull(barReader, store)

		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			l.Println("Unable to read part:", err)
			return err
		}

		if err == io.EOF && bytesRead == 0 {
			break
		}

		var bufferReader *bytes.Reader
		if bytesRead == partSize  {

			bufferReader = bytes.NewReader(store)
		} else {
			bufferReader = bytes.NewReader(store[:bytesRead])
		}


		partReq := &s3.UploadPartInput{
			Bucket: &bucketName,
			Key: &keyName,
			UploadId: upload.UploadId,
			PartNumber: &partNumber,
			ContentLength: aws.Int64(int64(bytesRead)),
			Body: bufferReader,
		}

		partResult, err := s3Client.UploadPart(partReq)

		if err != nil {
			l.Println("Unable to upload part:", err)
			return err
		}

		partMap = append(partMap, &s3.CompletedPart{
			PartNumber: aws.Int64(partNumber),
			ETag: partResult.ETag,
		})
	}

	bar.Finish()

	l.Println("Completing Upload.")

	completedUpload := &s3.CompletedMultipartUpload{
		Parts: partMap,
	}

	completionReq := &s3.CompleteMultipartUploadInput{
		Bucket: &bucketName,
		Key: &keyName,
		UploadId: upload.UploadId,
		MultipartUpload: completedUpload,
	}

	completion, err := s3Client.CompleteMultipartUpload(completionReq)

	if err != nil {
		l.Println("Unable to complete upload:", err)
	}

	l.Println("Completed upload:", *completion.ETag)


	return nil
}