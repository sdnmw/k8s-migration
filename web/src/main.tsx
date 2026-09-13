import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import './styles.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ConfigProvider
      locale={zhCN}
      theme={{
        token: {
          colorPrimary: '#0096FF',
          colorBgLayout: '#F7F8FA',
          colorBorder: '#E4E7EB',
          colorText: '#1F2937',
          colorTextSecondary: '#5B6573',
          colorSuccess: '#2DA771',
          colorWarning: '#E79B24',
          colorError: '#D94A4A',
          borderRadius: 6,
          controlHeight: 32,
          motionDurationFast: '0.12s',
          motionDurationMid: '0.18s',
          motionDurationSlow: '0.24s',
          fontSize: 14,
          fontFamily: 'Inter, "PingFang SC", "Microsoft YaHei", Arial, sans-serif',
        },
        components: {
          Layout: { headerBg: '#FFFFFF', siderBg: '#FFFFFF' },
          Menu: { itemHeight: 40, itemBorderRadius: 4, itemSelectedBg: '#EAF7FF', itemSelectedColor: '#007DD6' },
          Table: { headerBg: '#F7F8FA', headerColor: '#5B6573', rowHoverBg: '#F8FBFD', cellPaddingBlock: 13 },
          Card: { headerFontSize: 16 },
          Button: { borderRadius: 5, primaryShadow: 'none', defaultShadow: 'none', fontWeight: 500 },
          Tabs: { horizontalItemGutter: 24, titleFontSize: 14, inkBarColor: '#0096ff' },
          Drawer: { paddingLG: 24, footerPaddingBlock: 16 },
          Modal: { titleFontSize: 18 },
          Descriptions: { labelBg: '#F8FAFC', itemPaddingBottom: 16 },
          Form: { itemMarginBottom: 20, verticalLabelPadding: '0 0 6px' },
        },
      }}
    >
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </QueryClientProvider>
    </ConfigProvider>
  </StrictMode>,
)
