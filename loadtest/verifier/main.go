package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// must match what the admin endpoint seeds
const initialStock = 500

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(2)
}

func main() {
	ctx := context.TODO()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		fail("loading AWS config: %v", err)
	}
	dbClient := dynamodb.NewFromConfig(cfg)
	sqsClient := sqs.NewFromConfig(cfg)

	fmt.Println("Auditing FlashCart Database...")

	// find the tables and dlq by name
	productsTable, ordersTable := "", ""
	tablePages := dynamodb.NewListTablesPaginator(dbClient, &dynamodb.ListTablesInput{})
	for tablePages.HasMorePages() {
		page, err := tablePages.NextPage(ctx)
		if err != nil {
			fail("listing tables: %v", err)
		}
		for _, t := range page.TableNames {
			if strings.Contains(t, "ProductsTable") { productsTable = t }
			if strings.Contains(t, "OrdersTable") { ordersTable = t }
		}
	}
	if productsTable == "" || ordersTable == "" {
		fail("could not find the FlashCart tables in this account/region")
	}

	queues, err := sqsClient.ListQueues(ctx, &sqs.ListQueuesInput{QueueNamePrefix: aws.String("InfraStack-OrderDLQ")})
	if err != nil || len(queues.QueueUrls) == 0 {
		fail("could not find the dead-letter queue: %v", err)
	}
	dlqUrl := queues.QueueUrls[0]

	// count orders by status
	confirmed, failed, pending, other := 0, 0, 0, 0
	orderPages := dynamodb.NewScanPaginator(dbClient, &dynamodb.ScanInput{TableName: &ordersTable})
	for orderPages.HasMorePages() {
		page, err := orderPages.NextPage(ctx)
		if err != nil {
			fail("scanning orders: %v", err)
		}
		for _, item := range page.Items {
			status, _ := item["status"].(*types.AttributeValueMemberS)
			switch {
			case status == nil:
				other++
			case status.Value == "CONFIRMED":
				confirmed++
			case status.Value == "FAILED":
				failed++
			case status.Value == "PENDING":
				pending++
			default:
				other++
			}
		}
	}

	// remaining stock
	product, err := dbClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &productsTable,
		Key: map[string]types.AttributeValue{"productId": &types.AttributeValueMemberS{Value: "FLASH-TV-001"}},
	})
	if err != nil || product.Item == nil {
		fail("reading FLASH-TV-001: %v", err)
	}
	stockAttr, ok := product.Item["stock"].(*types.AttributeValueMemberN)
	if !ok {
		fail("FLASH-TV-001 has no numeric stock attribute")
	}
	stock, err := strconv.Atoi(stockAttr.Value)
	if err != nil {
		fail("parsing stock %q: %v", stockAttr.Value, err)
	}

	// dlq depth, visible plus in flight
	attrs, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: &dlqUrl,
		AttributeNames: []sqstypes.QueueAttributeName{
			sqstypes.QueueAttributeNameApproximateNumberOfMessages,
			sqstypes.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
		},
	})
	if err != nil {
		fail("reading DLQ attributes: %v", err)
	}
	visible, _ := strconv.Atoi(attrs.Attributes[string(sqstypes.QueueAttributeNameApproximateNumberOfMessages)])
	inFlight, _ := strconv.Atoi(attrs.Attributes[string(sqstypes.QueueAttributeNameApproximateNumberOfMessagesNotVisible)])
	dlqDepth := visible + inFlight

	total := confirmed + failed + pending + other
	fmt.Println("\n --- FLASH-SALE RESULTS --- 📊")
	fmt.Printf("Total Orders:                  %d\n", total)
	fmt.Printf("Confirmed (Sold):              %d\n", confirmed)
	fmt.Printf("Failed (Credit Card Declined): %d\n", failed)
	fmt.Printf("Pending (Still in Queue):      %d\n", pending)
	if other > 0 {
		fmt.Printf("Unknown status:                %d\n", other)
	}
	fmt.Printf("Remaining TV Stock:            %d\n", stock)
	fmt.Printf("Dead Letter Queue depth:       %d\n", dlqDepth)

	// declined orders give their unit back, so only confirmed and pending orders hold stock
	expected := initialStock - confirmed - pending
	fmt.Println("\nINVARIANT CHECKS:")
	ok = true
	check := func(pass bool, format string, args ...any) {
		mark := "PASS"
		if !pass {
			mark, ok = "FAIL", false
		}
		fmt.Printf("  [%s] %s\n", mark, fmt.Sprintf(format, args...))
	}
	check(stock == expected, "%d initial - %d confirmed - %d pending = %d, remaining stock is %d", initialStock, confirmed, pending, expected, stock)
	check(stock >= 0, "stock never went negative (%d)", stock)
	check(confirmed+pending <= initialStock, "units held by orders (%d) <= initial stock (%d)", confirmed+pending, initialStock)
	check(other == 0, "every order has a known status (%d unknown)", other)
	check(dlqDepth == 0, "dead-letter queue is empty (%d messages)", dlqDepth)
	if pending > 0 {
		fmt.Println("  Note: pending orders are still being processed. Re-run once the queue drains.")
	}

	if !ok {
		os.Exit(1)
	}
}
