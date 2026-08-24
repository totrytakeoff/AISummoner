import type { Device } from '../api/types'
import { ActivityIcon, CloseIcon, MaximizeIcon, RestoreIcon, TerminalIcon } from '../components/Icons'
import { StatusBadge } from '../components/StatusBadge'
import { TerminalPanel } from '../terminal/TerminalPanel'

export type WorkspaceDockTab = 'terminal' | 'activity'

interface WorkspaceDockProps {
  device: Device
  tab: WorkspaceDockTab
  terminalMounted: boolean
  maximized: boolean
  onTab: (tab: WorkspaceDockTab) => void
  onClose: () => void
  onToggleMaximized: () => void
}

function displayTime(value: string | null): string {
  if (!value) return '从未'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN')
}

export function WorkspaceDock(props: WorkspaceDockProps) {
  return (
    <div className="workspace-dock" id="workspace-components">
      <div className="dock-toolbar">
        <div className="dock-tabs" role="tablist" aria-label="组件标签">
          <button
            type="button"
            role="tab"
            aria-selected={props.tab === 'terminal'}
            disabled={!props.device.online}
            onClick={() => props.onTab('terminal')}
          ><TerminalIcon />终端</button>
          <button
            type="button"
            role="tab"
            aria-selected={props.tab === 'activity'}
            onClick={() => props.onTab('activity')}
          ><ActivityIcon />设备</button>
        </div>
        <button
          className="icon-button"
          type="button"
          aria-label={props.maximized ? '还原组件栏' : '最大化组件栏'}
          onClick={props.onToggleMaximized}
        >{props.maximized ? <RestoreIcon /> : <MaximizeIcon />}</button>
        <button className="icon-button" type="button" aria-label="关闭组件栏" onClick={props.onClose}><CloseIcon /></button>
      </div>
      <div className="dock-content">
        <section role="tabpanel" hidden={props.tab !== 'terminal'} aria-label="终端">
          {props.device.online
            ? props.terminalMounted && <TerminalPanel deviceID={props.device.id} />
            : <div className="centered-state">设备离线，无法打开终端。</div>}
        </section>
        <section className="device-activity" role="tabpanel" hidden={props.tab !== 'activity'} aria-label="设备信息">
          <div className="device-activity-heading">
            <div><span className="eyebrow">当前设备</span><h2>{props.device.name}</h2></div>
            <StatusBadge online={props.device.online} />
          </div>
          <dl className="activity-details">
            <div><dt>系统</dt><dd>{props.device.platform} / {props.device.arch}</dd></div>
            <div><dt>客户端</dt><dd>{props.device.client_version}</dd></div>
            <div><dt>最近在线</dt><dd>{displayTime(props.device.last_seen_at)}</dd></div>
            <div><dt>设备 ID</dt><dd className="mono">{props.device.id}</dd></div>
          </dl>
          <p className="muted tiny">后续能力版本将在此加入经过脱敏的实时操作记录。</p>
        </section>
      </div>
    </div>
  )
}
