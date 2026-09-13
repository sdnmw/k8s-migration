import '@testing-library/jest-dom/vitest'

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  }),
})

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

Object.defineProperty(window, 'ResizeObserver', { writable: true, value: TestResizeObserver })
Object.defineProperty(globalThis, 'ResizeObserver', { writable: true, value: TestResizeObserver })

const jsdomGetComputedStyle = window.getComputedStyle.bind(window)
window.getComputedStyle = (element: Element) => jsdomGetComputedStyle(element)
