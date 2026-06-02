import { useContext } from 'react';
import { SSEContext } from '../context/SSEContextObject';

// useSSE is a custom hook to consume the real-time SSE context safely.
export const useSSE = () => {
  const context = useContext(SSEContext);
  if (!context) {
    throw new Error('useSSE must be used within an SSEProvider');
  }
  return context;
};
