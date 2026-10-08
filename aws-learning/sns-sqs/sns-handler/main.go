package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

func handler(ctx context.Context, snsEvent events.SNSEvent) error {
	for _, record := range snsEvent.Records {
		snsRecord := record.SNS
		fmt.Printf("[SNS-DIRECT] MessageId: %s\n", snsRecord.MessageID)
		fmt.Printf("[SNS-DIRECT] TopicArn:  %s\n", snsRecord.TopicArn)
		fmt.Printf("[SNS-DIRECT] Message:   %s\n", snsRecord.Message)
	}
	return nil
}

func main() {
	lambda.Start(handler)
}
