# FlashCart dashboard

React + TypeScript dashboard for the FlashCart flash sale, built with Vite and served from S3 and CloudFront.

It polls `GET /products/FLASH-TV-001` every second for the remaining stock, places orders with `POST /orders` (each click sends a fresh `Idempotency-Key`), and restocks the sale through `POST /admin/products` with the admin token pasted into the footer.

The API base URL is hardcoded as `API_URL` in [`src/App.tsx`](src/App.tsx); set it to your stack's `ApiUrl` output before building.

```bash
npm ci
npm run dev    # local dev server
npm run lint   # oxlint
npm run build  # type-check and build to dist/ for the CDK deploy
```
