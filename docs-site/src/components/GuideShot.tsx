import React from 'react'
import useBaseUrl from '@docusaurus/useBaseUrl'
import screenshots from '../data/guide-screenshots.json'

type Props = { id: number; focus?: { x: number; y: number; label: string } }

export default function GuideShot({ id, focus }: Props) {
  const shot = screenshots.find((item) => item.id === id)
  const src = useBaseUrl(`/img/guide/${shot?.file ?? ''}`)
  if (!shot) throw new Error(`Unknown guide screenshot ${id}`)
  return <figure className="guide-figure">
    <div className="guide-image">
      <img src={src} alt={`图 ${id}：${shot.title}`} loading="lazy" width="1440" height="900" />
      {focus && <span className="guide-callout" style={{ left: `${focus.x}%`, top: `${focus.y}%` }} title={focus.label} aria-label={`标注 1：${focus.label}`}>1</span>}
    </div>
    <figcaption>图 {id} · {shot.title}{focus ? `（标注 1：${focus.label}）` : ''}</figcaption>
    {focus && <details className="guide-focus">
      <summary>查看局部放大：{focus.label}</summary>
      <div role="img" aria-label={`图 ${id} 局部放大：${focus.label}`} style={{ backgroundImage: `url(${src})`, backgroundPosition: `${focus.x}% ${focus.y}%` }} />
    </details>}
    <p><strong>操作：</strong>{shot.action}</p>
    <p><strong>预期结果：</strong>{shot.expected}</p>
    <p><strong>未达到预期时：</strong>{shot.failure}</p>
  </figure>
}
