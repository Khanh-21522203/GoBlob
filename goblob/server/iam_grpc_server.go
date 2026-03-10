package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	iampb "GoBlob/goblob/pb/iam_pb"
	s3iam "GoBlob/goblob/s3api/iam"
)

// IAMGRPCServer exposes IAMService on the filer gRPC server.
type IAMGRPCServer struct {
	iampb.UnimplementedIAMServiceServer
	fs *FilerServer
}

// NewIAMGRPCServer creates a filer-backed IAM gRPC server.
func NewIAMGRPCServer(fs *FilerServer) *IAMGRPCServer {
	return &IAMGRPCServer{fs: fs}
}

func (s *IAMGRPCServer) GetS3ApiConfiguration(ctx context.Context, _ *iampb.GetS3ApiConfigurationRequest) (*iampb.GetS3ApiConfigurationResponse, error) {
	iamMgr, err := s.newIAMManager()
	if err != nil {
		return nil, err
	}
	return &iampb.GetS3ApiConfigurationResponse{S3ApiConfiguration: iamMgr.GetConfiguration()}, nil
}

func (s *IAMGRPCServer) PutS3ApiConfiguration(ctx context.Context, req *iampb.PutS3ApiConfigurationRequest) (*iampb.PutS3ApiConfigurationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	cfg := req.GetS3ApiConfiguration()
	if err := s3iam.ValidateConfiguration(cfg); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid IAM config: %v", err)
	}

	iamMgr, err := s.newIAMManager()
	if err != nil {
		return nil, err
	}
	if err := iamMgr.Reload(cfg); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "reload IAM config: %v", err)
	}
	if err := iamMgr.SaveToFiler(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "save IAM config: %v", err)
	}
	return &iampb.PutS3ApiConfigurationResponse{}, nil
}

func (s *IAMGRPCServer) GetS3ApiConfigurations(req *iampb.GetS3ApiConfigurationsRequest, stream iampb.IAMService_GetS3ApiConfigurationsServer) error {
	_ = req
	iamMgr, err := s.newIAMManager()
	if err != nil {
		return err
	}
	return stream.Send(&iampb.GetS3ApiConfigurationsResponse{S3ApiConfiguration: iamMgr.GetConfiguration()})
}

func (s *IAMGRPCServer) newIAMManager() (*s3iam.IdentityAccessManagement, error) {
	if s == nil || s.fs == nil || s.fs.filer == nil || s.fs.filer.Store == nil {
		return nil, status.Error(codes.Unavailable, "filer not initialized")
	}
	iamMgr, err := s3iam.NewIdentityAccessManagement(s.fs.filer.Store, s.fs.logger)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "initialize IAM manager: %v", err)
	}
	if iamMgr == nil {
		return nil, status.Error(codes.Internal, "IAM manager is nil")
	}
	return iamMgr, nil
}
