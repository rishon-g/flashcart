package inventory

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// We define what an Order looks like
type Order struct {
	OrderID   string
	ProductID string
	Qty       int
}

// Reserve atomically decrements stock and creates an order
func Reserve(ctx context.Context, db *dynamodb.Client, productsTable string, ordersTable string, order Order) error {

	// 1. OPERATION ONE: Update the Product Stock
	updateStockOp := &types.TransactWriteItem{
		Update: &types.Update{
			TableName: aws.String(productsTable),
			Key: map[string]types.AttributeValue{
				"productId": &types.AttributeValueMemberS{Value: order.ProductID},
			},
			// The Math: set stock = stock - qty
			UpdateExpression: aws.String("SET stock = stock - :qty"),
			// THE RULE: Only do this if stock >= qty (Prevents Overselling!)
			ConditionExpression: aws.String("stock >= :qty"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":qty": &types.AttributeValueMemberN{Value: "1"}, // We are assuming 1 item per order for now
			},
		},
	}

	// 2. OPERATION TWO: Insert the Order Record
	insertOrderOp := &types.TransactWriteItem{
		Put: &types.Put{
			TableName: aws.String(ordersTable),
			Item: map[string]types.AttributeValue{
				"orderId":   &types.AttributeValueMemberS{Value: order.OrderID},
				"productId": &types.AttributeValueMemberS{Value: order.ProductID},
				"status":    &types.AttributeValueMemberS{Value: "PENDING"},
				"createdAt": &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
			},
			// THE RULE: Only save this if orderId doesn't exist yet (Prevents Double Charges!)
			ConditionExpression: aws.String("attribute_not_exists(orderId)"),
		},
	}

	// 3. EXECUTE THE TRANSACTION
	// Send both operations to DynamoDB. They both succeed, or they both fail together.
	_, err := db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			*updateStockOp,
			*insertOrderOp,
		},
	})

	return err
}