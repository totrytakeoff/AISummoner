export function StatusBadge({ online }: { online: boolean }) {
  return (
    <span className={`status-badge ${online ? 'online' : 'offline'}`}>
      <span className="status-dot" aria-hidden="true" />
      {online ? '在线' : '离线'}
    </span>
  )
}
