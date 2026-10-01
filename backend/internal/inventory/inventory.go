package inventory

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// an order for a single product
type Order struct {
	OrderID   string
	ProductID string
	Qty       int
}

// takes stock and creates the order in one transaction
func Reserve(ctx context.Context, db *dynamodb.Client, productsTable string, ordersTable string, order Order) error {

	// take one unit off the stock
	updateStockOp := &types.TransactWriteItem{
		Update: &types.Update{
			TableName: aws.String(productsTable),
			Key: map[string]types.AttributeValue{
				"productId": &types.AttributeValueMemberS{Value: order.ProductID},
			},
			UpdateExpression: aws.String("SET stock = stock - :qty"),
			// fails if there isn't enough stock, which is what stops overselling
			ConditionExpression: aws.String("stock >= :qty"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":qty": &types.AttributeValueMemberN{Value: "1"}, // always 1 unit per order for now
			},
		},
	}

	// create the order
	insertOrderOp := &types.TransactWriteItem{
		Put: &types.Put{
			TableName: aws.String(ordersTable),
			Item: map[string]types.AttributeValue{
				"orderId":   &types.AttributeValueMemberS{Value: order.OrderID},
				"productId": &types.AttributeValueMemberS{Value: order.ProductID},
				"status":    &types.AttributeValueMemberS{Value: "PENDING"},
				"createdAt": &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
			},
			// fails if this idempotency key was already used
			ConditionExpression: aws.String("attribute_not_exists(orderId)"),
		},
	}

	// both writes succeed or neither does
	_, err := db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			*updateStockOp,
			*insertOrderOp,
		},
	})

	return err
}