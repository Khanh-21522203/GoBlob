package iam

// IAMConfigKey is the filer KV key used to store S3 IAM configuration.
var IAMConfigKey = []byte("__iam_config__")

// S3Action represents a single S3 API action.
type S3Action string

const (
	S3ActionGetObject             S3Action = "s3:GetObject"
	S3ActionHeadObject            S3Action = "s3:HeadObject"
	S3ActionPutObject             S3Action = "s3:PutObject"
	S3ActionDeleteObject          S3Action = "s3:DeleteObject"
	S3ActionListBucket            S3Action = "s3:ListBucket"
	S3ActionListAllMyBuckets      S3Action = "s3:ListAllMyBuckets"
	S3ActionCreateBucket          S3Action = "s3:CreateBucket"
	S3ActionDeleteBucket          S3Action = "s3:DeleteBucket"
	S3ActionCreateMultipart       S3Action = "s3:CreateMultipartUpload"
	S3ActionUploadPart            S3Action = "s3:UploadPart"
	S3ActionCompleteMultipart     S3Action = "s3:CompleteMultipartUpload"
	S3ActionAbortMultipart        S3Action = "s3:AbortMultipartUpload"
	S3ActionGetBucketVersioning   S3Action = "s3:GetBucketVersioning"
	S3ActionPutBucketVersioning   S3Action = "s3:PutBucketVersioning"
	S3ActionGetBucketPolicy       S3Action = "s3:GetBucketPolicy"
	S3ActionPutBucketPolicy       S3Action = "s3:PutBucketPolicy"
	S3ActionDeleteBucketPolicy    S3Action = "s3:DeleteBucketPolicy"
	S3ActionGetBucketCors         S3Action = "s3:GetBucketCors"
	S3ActionPutBucketCors         S3Action = "s3:PutBucketCors"
	S3ActionGetBucketLifecycle    S3Action = "s3:GetBucketLifecycleConfiguration"
	S3ActionPutBucketLifecycle    S3Action = "s3:PutBucketLifecycleConfiguration"
	S3ActionDeleteBucketLifecycle S3Action = "s3:DeleteBucketLifecycleConfiguration"
	S3ActionGetObjectTagging      S3Action = "s3:GetObjectTagging"
	S3ActionPutObjectTagging      S3Action = "s3:PutObjectTagging"
	S3ActionDeleteObjectTagging   S3Action = "s3:DeleteObjectTagging"
	S3ActionAdmin                 S3Action = "Admin"
	S3ActionWildcard              S3Action = "*"
)

// Identity is one S3 principal.
type Identity struct {
	Name        string
	Credentials []*Credential
	Actions     []string
}

// Credential is an access key / secret key pair.
type Credential struct {
	AccessKey string
	SecretKey string
}

// AuthResult represents access-key authentication output.
type AuthResult struct {
	Identity  *Identity
	SecretKey string
	IsAnon    bool
	ErrorCode string
}
