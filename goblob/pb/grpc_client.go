package pb

import (
	"GoBlob/goblob/pb/filer_pb"
	"GoBlob/goblob/pb/master_pb"
	"GoBlob/goblob/pb/volume_server_pb"

	"google.golang.org/grpc"
)

// WithMasterServerClient dials the master server, calls fn, and closes the connection.
func WithMasterServerClient(masterAddr string, grpcDialOption grpc.DialOption, fn func(master_pb.MasterServiceClient) error) error {
	conn, err := grpc.NewClient(masterAddr, grpcDialOption)
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(master_pb.NewMasterServiceClient(conn))
}

// WithVolumeServerClient dials the volume server, calls fn, and closes the connection.
func WithVolumeServerClient(addr string, grpcDialOption grpc.DialOption, fn func(volume_server_pb.VolumeServerClient) error) error {
	conn, err := grpc.NewClient(addr, grpcDialOption)
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(volume_server_pb.NewVolumeServerClient(conn))
}

// WithFilerClient dials the filer server, calls fn, and closes the connection.
func WithFilerClient(addr string, grpcDialOption grpc.DialOption, fn func(filer_pb.FilerServiceClient) error) error {
	conn, err := grpc.NewClient(addr, grpcDialOption)
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(filer_pb.NewFilerServiceClient(conn))
}
