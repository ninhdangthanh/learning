package grpcsvc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "grpcvshttp/server/gen/product/v1"
	"grpcvshttp/server/store"
)

type Service struct {
	productv1.UnimplementedProductServiceServer
	store *store.Store
}

func New(s *store.Store) *Service {
	return &Service{store: s}
}

func toProto(p store.Product) *productv1.Product {
	return &productv1.Product{Id: p.ID, Name: p.Name, Price: p.Price, Qty: p.Qty}
}

func translateError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

func (s *Service) ListProducts(ctx context.Context, req *productv1.ListProductsRequest) (*productv1.ListProductsResponse, error) {
	items := s.store.List()
	out := make([]*productv1.Product, 0, len(items))
	for _, p := range items {
		out = append(out, toProto(p))
	}
	return &productv1.ListProductsResponse{Products: out}, nil
}

func (s *Service) GetProduct(ctx context.Context, req *productv1.GetProductRequest) (*productv1.GetProductResponse, error) {
	p, err := s.store.Get(req.GetId())
	if err != nil {
		return nil, translateError(err)
	}
	return &productv1.GetProductResponse{Product: toProto(p)}, nil
}

func (s *Service) CreateProduct(ctx context.Context, req *productv1.CreateProductRequest) (*productv1.CreateProductResponse, error) {
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name không được rỗng")
	}
	p := s.store.Create(req.GetName(), req.GetPrice(), req.GetQty())
	return &productv1.CreateProductResponse{Product: toProto(p)}, nil
}

func (s *Service) UpdateProduct(ctx context.Context, req *productv1.UpdateProductRequest) (*productv1.UpdateProductResponse, error) {
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name không được rỗng")
	}
	p, err := s.store.Update(req.GetId(), req.GetName(), req.GetPrice(), req.GetQty())
	if err != nil {
		return nil, translateError(err)
	}
	return &productv1.UpdateProductResponse{Product: toProto(p)}, nil
}

func (s *Service) DeleteProduct(ctx context.Context, req *productv1.DeleteProductRequest) (*productv1.DeleteProductResponse, error) {
	if err := s.store.Delete(req.GetId()); err != nil {
		return nil, translateError(err)
	}
	return &productv1.DeleteProductResponse{Id: req.GetId()}, nil
}
