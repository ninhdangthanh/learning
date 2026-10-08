package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

func handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	for _, message := range sqsEvent.Records {
		fmt.Printf("[PAYMENT] Xử lý đơn hàng: %s\n", message.Body)
	}
	return nil
}

func main() {
	lambda.Start(handler)
}
