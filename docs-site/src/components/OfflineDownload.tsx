import React from 'react'
import useBaseUrl from '@docusaurus/useBaseUrl'

export default function OfflineDownload({path, label}: {path: string; label: string}) {
  return <a href={useBaseUrl(path)} download>{label}</a>
}
