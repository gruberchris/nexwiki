import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App.tsx'
import { SSEProvider } from './context/SSEContext.tsx'
import './index.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <SSEProvider>
      <App />
    </SSEProvider>
  </StrictMode>,
)
