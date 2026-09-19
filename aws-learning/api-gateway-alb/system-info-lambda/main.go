package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

type systemInfo struct {
	Service     string `json:"service"`
	Version     string `json:"version"`
	Environment string `json:"environment"`
	Region      string `json:"region"`
	RequestID   string `json:"requestId"`
	Source      string `json:"source"`
}

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	info := systemInfo{
		Service:     "order-system",
		Version:     "1.0.0",
		Environment: os.Getenv("ENVIRONMENT"),
		Region:      os.Getenv("AWS_REGION"),
		RequestID:   req.RequestContext.RequestID,
		Source:      "lambda",
	}

	body, _ := json.Marshal(info)

	return events.APIGatewayV2HTTPResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(body),
	}, nil
}

func main() {
	lambda.Start(handler)
}
