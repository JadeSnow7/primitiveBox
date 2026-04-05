import React from 'react'
import ReactDOM from 'react-dom/client'
import App from '@/App'
import { UserApp } from '@/UserApp'
import '@/index.css'

const isUserRoute = window.location.pathname.startsWith('/app')

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    {isUserRoute ? <UserApp /> : <App />}
  </React.StrictMode>
)
