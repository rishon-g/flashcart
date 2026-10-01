# FlashCart: Serverless Flash-Sale Checkout Platform

FlashCart is an event-driven, serverless e-commerce backend built to handle massive, concurrent traffic spikes (like a Black Friday flash sale) without overselling inventory or double-charging customers. 

https://github.com/user-attachments/assets/cd0d3ee8-b07c-4732-ad13-b512206da8d0

## The Architecture

This project is built purely on **AWS Managed Services** using **Infrastructure as Code (AWS CDK)**, with the business logic written in strictly-typed **Go**.

1. **Frontend:** A React/Vite dashboard hosted on Amazon S3 and distributed globally via CloudFront.
2. **API (Synchronous):** Amazon API Gateway routes traffic to a Go Lambda function.
3. **Database:** DynamoDB stores inventory and orders.
4. **The Outbox Pipeline (Asynchronous):** DynamoDB Streams capture new orders and route them through EventBridge Pipes into an SQS Queue.
5. **Background Worker:** A second Go Lambda function pulls from SQS to process payments, executing compensating transactions on failure and routing poison pills to a Dead-Letter Queue (DLQ).

## Core Engineering Concepts Implemented

*   **Concurrency & Oversell Protection:** Utilized DynamoDB Conditional Writes (`stock >= :qty`) to implement optimistic locking.
*   **Idempotency:** Prevented double-charges during network retries by enforcing `attribute_not_exists(orderId)`.
*   **Transactional Outbox Pattern:** Eliminated the dual-write problem. The API only writes to DynamoDB; AWS internal infrastructure (Streams + Pipes) guarantees delivery to the SQS fulfillment queue.
*   **Self-Healing & Compensation:** The background worker simulates third-party payment processing. If a payment declines, a Compensating Transaction safely restores the item to the database inventory.
*   **Observability:** Integrated AWS CloudWatch Embedded Metric Format (EMF) to generate real-time zero-latency custom graphs for Orders Placed, Payment Failures, and Sold Out Rejections.
*   **CI/CD & Security:** Fully automated deployments via GitHub Actions, authenticated securely to AWS using **OpenID Connect (OIDC)** (no long-lived IAM access keys).

## Load Testing Benchmarks

I load-tested the API using **k6** to simulate a flash sale traffic spike of 1,000 concurrent users. 

*   **Throughput:** Survived a peak of **6,920 requests per second**.
*   **API Latency:** Optimized Lambda memory allocation to vertically scale CPU, achieving an average response time of **132ms** (p95: 194ms) under extreme load.
*   **Data Integrity:** A custom Go auditing script verified the database invariants post-test. Out of 145,000+ total requests, exactly 510 unique orders were processed. 418 succeeded, 85 payments were declined (and inventory safely restored), resulting in exactly **0 oversold units and 0 duplicate charges.**

## Tech Stack
*   **Backend:** Go (Golang), AWS Lambda, API Gateway
*   **Database & Queues:** DynamoDB, SQS, EventBridge Pipes
*   **Infrastructure:** AWS CDK (TypeScript), GitHub Actions
*   **Frontend:** React, TypeScript, Vite, TanStack Query, Axios
