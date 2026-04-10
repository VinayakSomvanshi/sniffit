import { GlobalRegistrator } from "@happy-dom/global-registrator";
import * as matchers from "@testing-library/jest-dom/matchers";
import { expect } from "bun:test";

GlobalRegistrator.register();
expect.extend(matchers);

window.location.href = "http://localhost/";

class MockEventSource {
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((ev: any) => void) | null = null;
  constructor(public url: string) {
    (global as any).lastEventSource = this;
    setTimeout(() => this.onopen?.(), 0);
  }
  addEventListener = () => {};
  removeEventListener = () => {};
  close = () => {};
  emit(data: any) {
    this.onmessage?.({ data: JSON.stringify(data) });
  }
}

(global as any).EventSource = MockEventSource;

(global as any).fetch = (url: string) => {
  return Promise.resolve({
    ok: true,
    json: () => Promise.resolve([]),
    text: () => Promise.resolve(""),
    blob: () => Promise.resolve(new Blob()),
  });
};

(global as any).confirm = () => true;
(global as any).URL.createObjectURL = () => 'blob:mock-url';
(global as any).URL.revokeObjectURL = () => {};
