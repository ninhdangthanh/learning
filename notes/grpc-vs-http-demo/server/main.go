package main

import (
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	productv1 "grpcvshttp/server/gen/product/v1"
	"grpcvshttp/server/grpcsvc"
	"grpcvshttp/server/rest"
	"grpcvshttp/server/store"
)

const (
	restAddr    = ":8080"
	grpcAddr    = ":50051"
	grpcWebAddr = ":8082"
)

func main() {
	shared := store.New()

	grpcServer := grpc.NewServer()
	productv1.RegisterProductServiceServer(grpcServer, grpcsvc.New(shared))
	reflection.Register(grpcServer)

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		log.Printf("REST/JSON      http://localhost%s/api/products", restAddr)
		if err := http.ListenAndServe(restAddr, rest.NewHandler(shared).Mux()); err != nil {
			log.Fatalf("REST server dừng: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		lis, err := net.Listen("tcp", grpcAddr)
		if err != nil {
			log.Fatalf("không mở được cổng gRPC: %v", err)
		}
		log.Printf("gRPC thuần     localhost%s  (browser KHÔNG gọi được cổng này)", grpcAddr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server dừng: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		wrapped := grpcweb.WrapServer(grpcServer,
			grpcweb.WithOriginFunc(func(origin string) bool { return true }),
		)
		log.Printf("gRPC-Web bridge http://localhost%s  (cầu nối cho browser)", grpcWebAddr)
		if err := http.ListenAndServe(grpcWebAddr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if wrapped.IsGrpcWebRequest(r) || wrapped.IsAcceptableGrpcCorsRequest(r) || wrapped.IsGrpcWebSocketRequest(r) {
				wrapped.ServeHTTP(w, r)
				return
			}
			http.Error(w, "cổng này chỉ nhận gRPC-Web", http.StatusBadRequest)
		})); err != nil {
			log.Fatalf("gRPC-Web server dừng: %v", err)
		}
	}()

	wg.Wait()
}
