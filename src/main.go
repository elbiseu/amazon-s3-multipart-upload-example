package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	maxContentSize    int64 = 1024 * 1024 * 2500 // 2500 MB
	minUploadPartSize int64 = 1024 * 1024 * 5    // 5 MB
)

var (
	client *s3.Client
	bucket = os.Getenv("BUCKET")
)

type Link struct {
	URL string `json:"url"`
}

type Message struct {
	Key   string `json:"key"`
	Links []Link `json:"links"`
}

func FileHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		w.Header().Set("Accept", "application/octet-stream")
		w.Header().Set("Content-Type", "application/json")
		contentType := r.Header.Get("Content-Type")
		if !strings.HasPrefix(contentType, "image/") && !strings.HasPrefix(contentType, "video/") {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		if r.ContentLength > maxContentSize {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		filenameExtension := func(contentType string) string {
			switch contentType {
			case "image/gif":
				return ".gif"
			case "image/jpeg":
				return ".jpeg"
			case "image/png":
				return ".png"
			case "image/tiff":
				return ".tiff"
			case "video/quicktime":
				return ".mov"
			case "video/mpeg":
				return ".mpeg"
			case "video/mp4 ":
				return ".mp4"
			case "video/webm":
				return ".webm"
			default:
				return ""
			}
		}(contentType)
		ctx := r.Context()
		multipartUploadOutput, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
			Bucket:                    aws.String(bucket),
			Key:                       aws.String(uuid.New().String() + filenameExtension),
			ACL:                       types.ObjectCannedACLPrivate,
			BucketKeyEnabled:          false,
			CacheControl:              nil,
			ContentDisposition:        nil,
			ContentEncoding:           nil,
			ContentLanguage:           nil,
			ContentType:               aws.String(contentType),
			ExpectedBucketOwner:       nil,
			Expires:                   nil,
			GrantFullControl:          nil,
			GrantRead:                 nil,
			GrantReadACP:              nil,
			GrantWriteACP:             nil,
			Metadata:                  nil,
			ObjectLockLegalHoldStatus: "",
			ObjectLockMode:            "",
			ObjectLockRetainUntilDate: nil,
			RequestPayer:              "",
			SSECustomerAlgorithm:      nil,
			SSECustomerKey:            nil,
			SSECustomerKeyMD5:         nil,
			SSEKMSEncryptionContext:   nil,
			SSEKMSKeyId:               nil,
			ServerSideEncryption:      "",
			StorageClass:              "",
			Tagging:                   nil,
			WebsiteRedirectLocation:   nil,
		})
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var buffer bytes.Buffer
		var completedParts []types.CompletedPart
		var lastPart bool
		var partNumber int32 = 1 // The first part number must always start with 1.
		for !lastPart {
			n, err := io.CopyN(&buffer, r.Body, minUploadPartSize)
			// The io.EOF error occurs when the stream has reached its end.
			if n == 0 || err == io.EOF {
				lastPart = true
			} else if err != nil {
				log.Println(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// If the buffer has the minimum required size or the current part is the last one,
			// a new part is stored in the bucket.
			uploadPartOutput, err := client.UploadPart(ctx, &s3.UploadPartInput{
				Bucket:               multipartUploadOutput.Bucket,
				Key:                  multipartUploadOutput.Key,
				PartNumber:           partNumber,
				UploadId:             multipartUploadOutput.UploadId,
				Body:                 bytes.NewReader(buffer.Bytes()),
				ContentLength:        int64(buffer.Len()),
				ContentMD5:           nil,
				ExpectedBucketOwner:  nil,
				RequestPayer:         "",
				SSECustomerAlgorithm: nil,
				SSECustomerKey:       nil,
				SSECustomerKeyMD5:    nil,
			})
			if err != nil {
				log.Println(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			completedParts = append(completedParts, types.CompletedPart{
				ETag:       uploadPartOutput.ETag,
				PartNumber: partNumber,
			})
			// The buffer is empty to the next parts.
			buffer.Reset()
			partNumber++
		}
		completeMultipartUploadOutput, err := client.CompleteMultipartUpload(ctx,
			&s3.CompleteMultipartUploadInput{
				Bucket:              multipartUploadOutput.Bucket,
				Key:                 multipartUploadOutput.Key,
				UploadId:            multipartUploadOutput.UploadId,
				ExpectedBucketOwner: nil,
				MultipartUpload: &types.CompletedMultipartUpload{
					Parts: completedParts,
				},
				RequestPayer: "",
			})
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		b, err := json.Marshal(Message{
			Key: *completeMultipartUploadOutput.Key,
			Links: []Link{
				{
					URL: *completeMultipartUploadOutput.Location,
				},
			},
		})
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		if _, err := w.Write(b); err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		return
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
}

func init() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalln(err)
	}
	client = s3.NewFromConfig(cfg)
}

func main() {
	serveMux := http.NewServeMux()
	serveMux.HandleFunc("/api/v1/file", FileHandler)
	if err := http.ListenAndServe(":8080", serveMux); err != nil {
		log.Fatalln(err)
	}
}
