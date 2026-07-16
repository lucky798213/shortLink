package grpcserver

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"short_url/internal/shortlink"
)

func toGRPCError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, shortlink.ErrInvalidOriginURL), errors.Is(err, shortlink.ErrInvalidExpireAt):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, shortlink.ErrNotFound):
		return status.Error(codes.NotFound, shortlink.ErrNotFound.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, context.Canceled.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	default:
		return status.Error(codes.Internal, "internal error")
	}
}
