import { useState } from 'react'
import { useQuery, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import axios from 'axios'
import './App.css'

// Your live AWS API URL!
const API_URL = 'https://3vqgxahtxk.execute-api.us-east-1.amazonaws.com'

const queryClient = new QueryClient()

function Dashboard() {
  const [message, setMessage] = useState('')

  // The Magic: This asks your AWS database for the TV stock every 1000ms (1 second)
  const { data: stock, isLoading } = useQuery({
    queryKey: ['tvStock'],
    queryFn: async () => {
      const res = await axios.get(`${API_URL}/products/FLASH-TV-001`)
      return res.data.stock
    },
    refetchInterval: 1000, 
  })

  const handleSeed = async () => {
    setMessage('Loading TVs into warehouse...')
    await axios.post(`${API_URL}/admin/products`)
    setMessage('Warehouse restocked to 500 TVs!')
  }

  const handleBuy = async () => {
    setMessage('Processing...')
    try {
      // Generate a random idempotency key for every click
      const key = `manual-click-${Math.random()}`
      await axios.post(`${API_URL}/orders`, null, {
        headers: { 'Idempotency-Key': key }
      })
      setMessage('Order Confirmed!')
    } catch (error: any) {
      if (error.response?.status === 409) {
        setMessage('SOLD OUT!')
      } else {
        setMessage('Error processing order.')
      }
    }
  }

  return (
    <div className="card">
      <h1>⚡ FlashCart Live Sale</h1>
      
      <div className="stock-display">
        <h2>TVs Remaining:</h2>
        <h1 className={stock === '0' ? 'sold-out' : 'in-stock'}>
          {isLoading ? '...' : stock}
        </h1>
      </div>

      <div className="buttons">
        <button onClick={handleBuy} disabled={stock === '0'}>BUY NOW</button>
        <button onClick={handleSeed} className="admin-btn">(Admin) Reset Stock</button>
      </div>

      <p className="message">{message}</p>
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