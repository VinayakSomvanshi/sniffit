import '@testing-library/jest-dom';
import { vi } from 'vitest';

// Happy DOM requires an absolute URL for everything if location isn't set perfectly
const BASE_URL = 'http://localhost';
window.location.href = `${BASE_URL}/`;

// Mock EventSource for SSE
class MockEventSource {
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((ev: any) => void) | null = null;
  
  constructor(public url: string) {
    (global as any).lastEventSource = this;
    setTimeout(() => this.onopen?.(), 0);
  }
  
  addEventListener = vi.fn();
  removeEventListener = vi.fn();
  close = vi.fn();

  emit(data: any) {
    this.onmessage?.({ data: JSON.stringify(data) });
  }
}

(global as any).EventSource = MockEventSource;

// Mock global fetch to handle relative URLs by prepending BASE_URL
const originalFetch = global.fetch;
global.fetch = vi.fn().mockImplementation((input: any, init?: any) => {
  let url = typeof input === 'string' ? input : input.url;
  if (url.startsWith('/')) {
    url = `${BASE_URL}${url}`;
  }
  return Promise.resolve({
    ok: true,
    json: () => Promise.resolve([]),
    text: () => Promise.resolve(""),
    blob: () => Promise.resolve(new Blob()),
  });
}) as any;

// Mock window.confirm
window.confirm = vi.fn(() => true);

// Mock URL.createObjectURL
global.URL.createObjectURL = vi.fn(() => 'blob:mock-url');
global.URL.revokeObjectURL = vi.fn();

// Mock window.confirm
window.confirm = vi.fn(() => true);

// Mock URL.createObjectURL
global.URL.createObjectURL = vi.fn(() => 'blob:mock-url');
global.URL.revokeObjectURL = vi.fn();
