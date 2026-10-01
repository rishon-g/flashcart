import { useState } from 'react'
import { useQuery, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import axios from 'axios'
import './App.css'

const API_URL = 'https://3vqgxahtxk.execute-api.us-east-1.amazonaws.com'
const PRODUCT_ID = 'FLASH-TV-001'
const TOTAL_STOCK = 500
const LOW_STOCK = 100
const SALE_PRICE = 199
const ORIGINAL_PRICE = 999

const queryClient = new QueryClient()

type Tone = 'info' | 'success' | 'error'

function Dashboard() {
  const [message, setMessage] = useState<{ text: string; tone: Tone } | null>(null)
  const [buying, setBuying] = useState(false)
  const [resetting, setResetting] = useState(false)
  const [adminToken, setAdminToken] = useState('')

  const { data: stock, isLoading, isError } = useQuery({
    queryKey: ['tvStock'],
    queryFn: async () => {
      const res = await axios.get(`${API_URL}/products/${PRODUCT_ID}`)
      return res.data.stock
    },
    refetchInterval: 1000,
  })

  const count = stock === undefined ? null : Number(stock)
  const soldOut = count === 0
  const low = count !== null && count > 0 && count <= LOW_STOCK
  const level = soldOut ? 'sold-out' : low ? 'low' : 'in-stock'
  const percent = count === null ? 0 : Math.min(100, (count / TOTAL_STOCK) * 100)
  const savings = ORIGINAL_PRICE - SALE_PRICE
  const discount = Math.round((savings / ORIGINAL_PRICE) * 100)

  const statusText = soldOut
    ? 'Every unit has been claimed.'
    : low
      ? 'Selling fast. Few units remain.'
      : 'In stock and ready to ship.'

  const handleSeed = async () => {
    setResetting(true)
    setMessage({ text: 'Loading TVs into the warehouse…', tone: 'info' })
    try {
      await axios.post(`${API_URL}/admin/products`, null, {
        headers: { 'X-Admin-Token': adminToken },
      })
      setMessage({ text: `Warehouse restocked to ${TOTAL_STOCK} TVs.`, tone: 'success' })
      queryClient.invalidateQueries({ queryKey: ['tvStock'] })
    } catch (error: any) {
      if (error.response?.status === 401) {
        setMessage({ text: 'Restock refused. Check the admin token.', tone: 'error' })
      } else {
        setMessage({ text: 'Restock failed. Check the API and try again.', tone: 'error' })
      }
    } finally {
      setResetting(false)
    }
  }

  const handleBuy = async () => {
    setBuying(true)
    setMessage({ text: 'Placing your order…', tone: 'info' })
    try {
      const key = `manual-click-${Math.random()}`
      await axios.post(`${API_URL}/orders`, null, {
        headers: { 'Idempotency-Key': key },
      })
      setMessage({ text: 'Order confirmed. Thanks for your purchase.', tone: 'success' })
    } catch (error: any) {
      if (error.response?.status === 409) {
        setMessage({ text: 'Sold out. All units were claimed before your order.', tone: 'error' })
      } else {
        setMessage({ text: 'Your order didn’t go through. Try again.', tone: 'error' })
      }
    } finally {
      setBuying(false)
      queryClient.invalidateQueries({ queryKey: ['tvStock'] })
    }
  }

  return (
    <div className="container">
      <article className="sale">
        <section className="details">
          <span className="live-badge">
            <span className="dot" aria-hidden="true" />
            Live flash sale
          </span>

          <h1 className="product-title">Flash TV 001</h1>
          <p className="product-sub">65" 4K OLED television</p>

          <div className="price-container">
            <span className="current-price">${SALE_PRICE}</span>
            <span className="original-price">${ORIGINAL_PRICE}</span>
          </div>
          <p className="savings">
            Save ${savings} ({discount}% off)
          </p>

          <dl className="specs">
            <div><dt>Screen size</dt><dd>65 inches</dd></div>
            <div><dt>Resolution</dt><dd>4K</dd></div>
            <div><dt>Panel</dt><dd>OLED</dd></div>
            <div><dt>SKU</dt><dd>{PRODUCT_ID}</dd></div>
          </dl>
        </section>

        <section className="stock-panel">
          <div className="stock-head">
            <h2>Units remaining</h2>
            <span className={`sync ${isError ? 'offline' : ''}`}>
              <span className="dot" aria-hidden="true" />
              {isError ? 'Offline' : 'Live'}
            </span>
          </div>

          <p className={`stock-count ${level}`}>{isLoading ? '…' : isError && count === null ? '–' : stock}</p>

          <div
            className="meter"
            role="progressbar"
            aria-label="Inventory remaining"
            aria-valuemin={0}
            aria-valuemax={TOTAL_STOCK}
            aria-valuenow={count ?? 0}
          >
            <div className={`meter-fill ${level}`} style={{ width: `${percent}%` }} />
          </div>
          <p className="stock-status">{count === null ? 'Checking inventory…' : statusText}</p>

          <button
            className="buy-btn"
            onClick={handleBuy}
            disabled={soldOut || buying || count === null}
          >
            {soldOut ? 'Sold out' : buying ? 'Placing order…' : `Buy now for $${SALE_PRICE}`}
          </button>

          <p className={`message ${message?.tone ?? ''}`} role="status" aria-live="polite">
            {message?.text}
          </p>
        </section>

        <footer className="admin-bar">
          <span>Admin tools. Restocks inventory to {TOTAL_STOCK} units.</span>
          <input
            type="password"
            className="admin-input"
            placeholder="Admin token"
            aria-label="Admin token"
            autoComplete="off"
            value={adminToken}
            onChange={(e) => setAdminToken(e.target.value)}
          />
          <button onClick={handleSeed} className="admin-btn" disabled={resetting || adminToken === ''}>
            {resetting ? 'Restocking…' : 'Reset warehouse'}
          </button>
        </footer>
      </article>
    </div>
  )
}

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Dashboard />
    </QueryClientProvider>
  )
}

export default App