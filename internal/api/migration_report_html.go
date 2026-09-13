package api

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"

	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
)

type migrationHTMLReport struct {
	Report   domainmigration.Report           `json:"report"`
	Topology domainmigration.TopologyEvidence `json:"topology"`
	Timeline domainmigration.StepTimeline     `json:"timeline"`
}

func renderMigrationHTMLReport(w io.Writer, value migrationHTMLReport) error {
	functions := template.FuncMap{
		"json": func(value any) string {
			encoded, _ := json.MarshalIndent(value, "", "  ")
			return string(encoded)
		},
		"time": func(value time.Time) string {
			if value.IsZero() {
				return "-"
			}
			return value.Local().Format("2006-01-02 15:04:05")
		},
		"duration": func(start time.Time, end *time.Time) string {
			if start.IsZero() {
				return "-"
			}
			stop := time.Now()
			if end != nil {
				stop = *end
			}
			return stop.Sub(start).Round(time.Second).String()
		},
		"statusClass": func(value any) string {
			status := strings.ToUpper(fmt.Sprint(value))
			switch status {
			case "COMPLETED", "SUCCEEDED", "SUCCESS":
				return "ok"
			case "FAILED", "MISSING", "ROLLBACK_FAILED":
				return "bad"
			case "WARNING", "SKIPPED", "RETRY_SCHEDULED", "CANCELLED", "ROLLED_BACK":
				return "warn"
			case "RUNNING", "CREATED", "AWAITING_CUTOVER":
				return "active"
			default:
				return "muted"
			}
		},
		"terminal": domainmigration.IsTerminal,
		"changed": func(values []domainmigration.MappingChange) bool {
			for _, value := range values {
				if value.Changed {
					return true
				}
			}
			return false
		},
		"countStatus": func(graph domainmigration.TopologyGraph, status string) int {
			count := 0
			for _, node := range graph.Nodes {
				if string(node.Status) == status {
					count++
				}
			}
			return count
		},
		"mappingClass": func(value *bool) string {
			if value == nil {
				return "active"
			}
			if *value {
				return "ok"
			}
			return "bad"
		},
		"mappingLabel": func(value *bool) string {
			if value == nil {
				return "待验证"
			}
			if *value {
				return "已生效"
			}
			return "未生效"
		},
	}
	tmpl, err := template.New("migration-report").Funcs(functions).Parse(migrationReportTemplate)
	if err != nil {
		return fmt.Errorf("parse migration report template: %w", err)
	}
	if err := tmpl.Execute(w, value); err != nil {
		return fmt.Errorf("render migration report: %w", err)
	}
	return nil
}

const migrationReportTemplate = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>迁移诊断报告 {{.Report.Run.ID}}</title>
<style>
:root{color-scheme:light;--ink:#1e293b;--sub:#64748b;--line:#dfe5ec;--bg:#f4f7fa;--blue:#1677ff;--green:#21a366;--red:#e5484d;--yellow:#d89614;--gray:#94a3b8}*{box-sizing:border-box}body{margin:0;background:var(--bg);font:14px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC",sans-serif;color:var(--ink)}header{background:linear-gradient(120deg,#0f3d73,#1677ff);color:#fff;padding:34px max(32px,calc((100vw - 1380px)/2))}h1{margin:0 0 8px;font-size:28px}header p{margin:3px 0;color:#dbeafe}.wrap{max-width:1380px;margin:22px auto;padding:0 22px}.grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.card{background:#fff;border:1px solid var(--line);border-radius:12px;padding:20px;margin-bottom:16px;box-shadow:0 1px 2px #0f172a0a}.metric strong{display:block;font-size:24px}.metric span,.sub{color:var(--sub)}h2{font-size:19px;margin:0 0 16px}h3{font-size:15px;margin:0 0 9px}.badge{display:inline-block;border-radius:12px;padding:2px 9px;font-size:12px;font-weight:650}.ok{color:#147d4d;background:#e8f7ef}.bad{color:#b42318;background:#fff0f0}.warn{color:#9a6700;background:#fff7d6}.active{color:#0758c9;background:#e8f2ff}.muted{color:#52606d;background:#eef2f6}.topology{display:grid;grid-template-columns:1fr 100px 1fr;gap:14px}.lane{border:1px solid var(--line);border-radius:10px;background:#f9fbfd;padding:14px}.lane-title{display:flex;justify-content:space-between;align-items:center;margin-bottom:12px}.nodes{display:grid;grid-template-columns:repeat(auto-fill,minmax(205px,1fr));gap:9px}.node{position:relative;border:1px solid var(--line);border-left:4px solid var(--gray);background:#fff;border-radius:8px;padding:10px 11px;min-height:72px}.node.ok{border-left-color:var(--green);color:var(--ink);background:#fff}.node.bad{border-left-color:var(--red);color:var(--ink);background:#fff}.node.warn{border-left-color:var(--yellow);color:var(--ink);background:#fff}.node.active{border-left-color:var(--blue);color:var(--ink);background:#fff}.node .kind{font-size:11px;color:var(--sub);text-transform:uppercase}.node .name{font-weight:650;word-break:break-all}.node .status{position:absolute;right:8px;top:7px;font-size:10px}.mapcol{display:flex;align-items:center;justify-content:center;color:var(--blue);font-weight:700;text-align:center}.mapping{display:grid;grid-template-columns:110px 1fr 28px 1fr 110px;gap:8px;align-items:center;padding:8px 0;border-bottom:1px solid #edf1f5}.mapping:last-child{border:0}.timeline{position:relative;margin-left:10px;padding-left:25px;border-left:2px solid #d9e2ec}.step{position:relative;margin:0 0 18px}.step:before{content:"";position:absolute;left:-32px;top:3px;width:12px;height:12px;border-radius:50%;background:var(--gray);border:3px solid #fff;box-shadow:0 0 0 1px var(--line)}.step.ok:before{background:var(--green)}.step.bad:before{background:var(--red)}.step.active:before{background:var(--blue)}.attempt{margin-top:7px;padding:8px 10px;background:#f7f9fb;border-radius:7px}.events{width:100%;border-collapse:collapse}.events th,.events td{text-align:left;border-bottom:1px solid var(--line);padding:8px;vertical-align:top}.events th{color:var(--sub);font-weight:600}.diagnosis{border-left:4px solid var(--blue);padding:13px 15px;background:#f2f7ff;border-radius:7px}.diagnosis.bad{border-left-color:var(--red);background:#fff5f5;color:var(--ink)}details{margin-top:14px}summary{cursor:pointer;font-weight:650}.footer{text-align:center;color:var(--sub);padding:10px 0 34px}@media(max-width:900px){.grid{grid-template-columns:1fr 1fr}.topology{grid-template-columns:1fr}.mapcol{padding:8px}.mapping{grid-template-columns:1fr}.mapping .arrow{text-align:center}}
</style></head><body>
<header><h1>SKS Migration Center 迁移诊断报告</h1><p>Run ID：{{.Report.Run.ID}}</p><p>{{if terminal .Report.Run.Status}}终态证据快照{{else}}执行中快照{{end}} · 报告生成于 {{time .Report.GeneratedAt}}</p></header>
<main class="wrap">
<section class="grid">
<div class="card metric"><span>任务结论</span><strong><span class="badge {{statusClass .Report.Run.Status}}">{{.Report.Run.Status}}</span></strong></div>
<div class="card metric"><span>执行耗时</span><strong>{{.Report.Summary.DurationSeconds}}s</strong></div>
<div class="card metric"><span>步骤完成</span><strong>{{.Report.Summary.CompletedStepCount}} / {{.Report.Summary.StepCount}}</strong></div>
<div class="card metric"><span>数据传输</span><strong>{{.Report.Summary.BytesTransferred}} B</strong></div>
</section>
<section class="card"><h2>故障诊断</h2><div class="diagnosis {{statusClass .Timeline.Diagnosis.State}}"><strong>{{.Timeline.Diagnosis.Title}}</strong>{{if .Timeline.Diagnosis.Reason}}<div>{{.Timeline.Diagnosis.Reason}}</div>{{end}}{{if .Timeline.Diagnosis.Remediation}}<div class="sub">建议：{{.Timeline.Diagnosis.Remediation}}</div>{{end}}{{if .Timeline.Diagnosis.LastSuccessfulStep}}<div class="sub">最后成功步骤：{{.Timeline.Diagnosis.LastSuccessfulStep}}</div>{{end}}</div></section>
<section class="card"><h2>源端与目标端资源拓扑</h2>
<div class="topology"><div class="lane"><div class="lane-title"><h3>源端 · {{.Topology.Source.Name}}</h3><span>{{len .Topology.Source.Nodes}} 个资源</span></div><div class="nodes">{{range .Topology.Source.Nodes}}<details class="node {{statusClass .Status}}"><summary><span class="badge status {{statusClass .Status}}">{{.Status}}</span><div class="kind">{{.Kind}}</div><div class="name">{{.Name}}</div><div class="sub">{{.Namespace}}</div></summary><pre>{{json .Attributes}}</pre></details>{{else}}<span class="sub">没有源资源证据</span>{{end}}</div></div>
<div class="mapcol">资源<br>映射<br>→</div>
<div class="lane"><div class="lane-title"><h3>目标端 · {{.Topology.Target.Name}}</h3><span>{{len .Topology.Target.Nodes}} 个资源</span></div><div class="nodes">{{range .Topology.Target.Nodes}}<details class="node {{statusClass .Status}}"><summary><span class="badge status {{statusClass .Status}}">{{.Status}}</span><div class="kind">{{.Kind}}</div><div class="name">{{.Name}}</div><div class="sub">{{.Namespace}}</div>{{if changed .MappingChanges}}<span class="badge active">已映射</span>{{end}}{{if .Message}}<div class="sub">{{.Message}}</div>{{end}}</summary><pre>{{json .Attributes}}</pre>{{range .MappingChanges}}<div class="sub">{{.Type}}：{{.SourceValue}} → {{.TargetValue}}（{{mappingLabel .Applied}}）</div>{{end}}</details>{{else}}<span class="sub">没有目标资源证据</span>{{end}}</div></div></div>
{{if .Topology.EvidenceLimitations}}<p class="sub">证据说明：{{range .Topology.EvidenceLimitations}}{{.}}；{{end}}</p>{{end}}
</section>
<section class="card"><h2>映射生效证据</h2>{{range .Topology.Mappings}}{{range .Changes}}<div class="mapping"><strong>{{.Type}}</strong><code>{{.SourceValue}}</code><span class="arrow">→</span><code>{{.TargetValue}}</code><span class="badge {{mappingClass .Applied}}">{{mappingLabel .Applied}}</span></div>{{end}}{{else}}<span class="sub">没有字段映射变更</span>{{end}}</section>
<section class="card"><h2>步骤执行时序</h2><div class="timeline">{{range .Timeline.Steps}}<div class="step {{statusClass .Step.Status}}"><h3>{{.Step.Type}} <span class="badge {{statusClass .Step.Status}}">{{.Step.Status}}</span></h3><div class="sub">{{if .Step.StartedAt}}{{time .Step.StartedAt}} · {{duration .Step.StartedAt .Step.CompletedAt}}{{else}}尚未开始{{end}}</div>{{if .Step.Summary}}<div>{{.Step.Summary}}</div>{{end}}{{range .Attempts}}<div class="attempt"><strong>第 {{.Attempt}} 次尝试</strong> <span class="badge {{statusClass .Status}}">{{.Status}}</span> · {{time .StartedAt}} · {{duration .StartedAt .CompletedAt}}{{if .ErrorMessage}}<div class="bad">{{.ErrorMessage}}</div>{{end}}{{if .Diagnostic}}<div class="sub">{{.Diagnostic}}</div>{{end}}</div>{{end}}</div>{{end}}</div></section>
<section class="card"><h2>卷迁移与校验</h2><table class="events"><thead><tr><th>源卷</th><th>目标 PVC</th><th>方式</th><th>进度</th><th>状态</th><th>校验</th></tr></thead><tbody>{{range .Report.VolumeTransfers}}<tr><td>{{.SourceVolume}}</td><td>{{.TargetVolume}}</td><td>{{.Engine}}</td><td>{{.TransferredBytes}} / {{.TotalBytes}}</td><td><span class="badge {{statusClass .Status}}">{{.Status}}</span></td><td>{{.ChecksumStatus}}</td></tr>{{else}}<tr><td colspan="6" class="sub">没有独立卷传输记录</td></tr>{{end}}</tbody></table></section>
<section class="card"><h2>关键事件时间线</h2><table class="events"><thead><tr><th>时间</th><th>级别</th><th>组件/事件</th><th>技术摘要</th></tr></thead><tbody>{{range .Report.Events}}<tr><td>{{time .CreatedAt}}</td><td><span class="badge {{statusClass .Severity}}">{{.Severity}}</span></td><td>{{.Type}}</td><td>{{.Message}}</td></tr>{{else}}<tr><td colspan="4" class="sub">没有事件记录</td></tr>{{end}}</tbody></table></section>
<section class="card"><h2>任务信息</h2><div class="grid"><div><span class="sub">MigrationPlan</span><br>{{.Report.Plan.ID}}</div><div><span class="sub">运行序号</span><br>#{{.Report.Run.RunNumber}}</div><div><span class="sub">切流确认</span><br>{{.Report.Summary.CutoverConfirmed}}</div><div><span class="sub">源端恢复</span><br>{{.Report.Summary.SourceRollbackWasInvoked}}</div><div><span class="sub">源快照</span><br>{{time .Topology.SourceCapturedAt}}</div><div><span class="sub">目标快照</span><br>{{if .Topology.TargetCapturedAt}}{{time .Topology.TargetCapturedAt}}{{else}}-{{end}}</div>{{if .Topology.CurrentObservation}}<div><span class="sub">当前状态检查</span><br>{{time .Topology.CurrentObservation.CheckedAt}}</div><div><span class="sub">漂移资源</span><br>{{.Topology.CurrentObservation.Drifted}}</div>{{end}}</div></section>
</main><div class="footer">本文件完全自包含，可离线打开。凭证、Secret 内容和令牌不会进入报告。</div>
<script>document.querySelectorAll('table.events').forEach(function(t){var rows=t.querySelectorAll('tbody tr');if(rows.length>50){for(var i=50;i<rows.length;i++)rows[i].hidden=true;var b=document.createElement('button');b.textContent='展开全部 '+rows.length+' 条';b.onclick=function(){rows.forEach(function(r){r.hidden=false});b.remove()};t.after(b)}});</script>
</body></html>`
