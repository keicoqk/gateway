// Example: minimal gRPC service for the gateway to call over HTTP.
package main

import (
	"context"
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/keicoqk/gateway/example/pb"
)

type server struct {
	pb.UnimplementedEchoServiceServer
}

func (s *server) Echo(ctx context.Context, req *pb.EchoRequest) (*pb.EchoResponse, error) {
	return &pb.EchoResponse{Message: "echo: " + req.Message}, nil
}

func main() {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatal(err)
	}
	s := grpc.NewServer()
	pb.RegisterEchoServiceServer(s, &server{})
	reflection.Register(s)
	log.Println("gRPC server listening on :50051")
	if err := s.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
