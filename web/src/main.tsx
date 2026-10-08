import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';

import App from './App';
import AuthProvider from './auth/AuthProvider';
import { applyTheme, readStoredTheme } from './lib/theme';
import './styles/global.css';

const rootElement = document.getElementById('root');
if (!rootElement) {
  throw new Error('Missing #root element');
}

// Apply the saved appearance before the first paint so a dark-theme user never
// sees a light flash.
applyTheme(readStoredTheme());

createRoot(rootElement).render(
  <StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <App />
      </AuthProvider>
    </BrowserRouter>
  </StrictMode>,
);
