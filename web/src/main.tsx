import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { AppTheme } from '@/components/shell/AppTheme';
import { App } from './App';
import './styles.css';

const root = document.getElementById('root');
if (!root) throw new Error('missing #root');
if (new URLSearchParams(window.location.search).has('electron')) document.documentElement.classList.add('electron');
createRoot(root).render(
  <StrictMode>
    <AppTheme>
      <BrowserRouter><App /></BrowserRouter>
    </AppTheme>
  </StrictMode>,
);
