import { Navigate, Route, Routes } from 'react-router-dom';
import { Dashboard } from '@/components/shell/Dashboard';

export function App() {
  return (
    <Routes>
      <Route path="/" element={<Dashboard />} />
      <Route path="/sessions/:sessionId" element={<Dashboard />} />
      <Route path="/settings" element={<Dashboard />} />
      <Route path="/settings/plugins" element={<Dashboard />} />
      <Route path="/plugins/:pluginId/*" element={<Dashboard />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
