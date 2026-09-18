package inventory

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// setupLocalDB connects to Docker and creates blank tables for our test
func setupLocalDB(ctx context.Context) *dynamodb.Client {
	// Connect to localhost:8000 instead of the real AWS cloud
	cfg, _ := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("dummy", "dummy", "")),
	)
	db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String("http://localhost:8000")
	})

	// Create Products Table
	db.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("Products-Test"),
		KeySchema: []types.KeySchemaElement{{AttributeName: aws.String("productId"), KeyType: types.KeyTypeHash}},
		AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String("productId"), AttributeType: types.ScalarAttributeTypeS}},
		BillingMode: types.BillingModePayPerRequest,
	})

	// Create Orders Table
	db.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("Orders-Test"),
		KeySchema: []types.KeySchemaElement{{AttributeName: aws.String("orderId"), KeyType: types.KeyTypeHash}},
		AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String("orderId"), AttributeType: types.ScalarAttributeTypeS}},
		BillingMode: types.BillingModePayPerRequest,
	})

	return db
}

func TestOversellPrevention(t *testing.T) {
	ctx := context.Background()
	db := setupLocalDB(ctx)

	productsTable := "Products-Test"
	ordersTable := "Orders-Test"
	productID := "FLASH-TV-001"

	// 1. SEED THE DATABASE: Give the store exactly 100 TVs
	db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(productsTable),
		Item: map[string]types.AttributeValue{
			"productId": &types.AttributeValueMemberS{Value: productID},
			"stock":     &types.AttributeValueMemberN{Value: "100"},
		},
	})

	// 2. PREPARE THE CONCURRENT RACE
	totalShoppers := 500
	var successfulPurchases int32 = 0
	var failedPurchases int32 = 0

	var wg sync.WaitGroup // This waits for all 500 threads to finish

	// 3. LAUNCH 500 SHOPPERS AT THE EXACT SAME TIME
	for i := 0; i < totalShoppers; i++ {
		wg.Add(1)
		
		// The "go" keyword spawns a Goroutine (a lightweight thread)
		go func(shopperID int) {
			defer wg.Done() // Tell the WaitGroup we finished when this thread exits

			order := Order{
				OrderID:   fmt.Sprintf("ORDER-%d", shopperID),
				ProductID: productID,
				Qty:       1,
			}

			// Try to buy the TV!
			err := Reserve(ctx, db, productsTable, ordersTable, order)

			if err == nil {
				// Thread-safe way to add 1 to success
				atomic.AddInt32(&successfulPurchases, 1)
			} else {
				// Thread-safe way to add 1 to fails
				atomic.AddInt32(&failedPurchases, 1)
			}
		}(i)
	}

	// 4. WAIT FOR THE DUST TO SETTLE
	wg.Wait()

	// 5. DID WE OVERSELL? (The actual tests)
	t.Logf("Successful purchases: %d", successfulPurchases)
	t.Logf("Failed purchases (Sold Out): %d", failedPurchases)

	if successfulPurchases != 100 {
		t.Errorf("FATAL: Expected exactly 100 successes, but got %d. WE OVERSOLD!", successfulPurchases)
	}

	// Double check the database says stock is exactly 0
	result, _ := db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(productsTable),
		Key: map[string]types.AttributeValue{"productId": &types.AttributeValueMemberS{Value: productID}},
	})
	
	finalStock := result.Item["stock"].(*types.AttributeValueMemberN).Value
	if finalStock != "0" {
		t.Errorf("FATAL: Expected database stock to be 0, but it is %s", finalStock)
	}
}