import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>

function IconBase({ children, ...props }: IconProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      width="18"
      height="18"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      {...props}
    >
      {children}
    </svg>
  )
}

export function AISummonerMark(props: IconProps) {
  return (
    <IconBase {...props}>
      <path d="M5.5 18.5 12 4l6.5 14.5" />
      <path d="M8 13.2h8" />
      <path d="m16.8 5.3.7 1.7 1.7.7-1.7.7-.7 1.7-.7-1.7-1.7-.7 1.7-.7.7-1.7Z" />
    </IconBase>
  )
}

export function ChevronLeftIcon(props: IconProps) {
  return <IconBase {...props}><path d="m15 18-6-6 6-6" /></IconBase>
}

export function ChevronRightIcon(props: IconProps) {
  return <IconBase {...props}><path d="m9 18 6-6-6-6" /></IconBase>
}

export function ChevronDownIcon(props: IconProps) {
  return <IconBase {...props}><path d="m6 9 6 6 6-6" /></IconBase>
}

export function PlusIcon(props: IconProps) {
  return <IconBase {...props}><path d="M12 5v14M5 12h14" /></IconBase>
}

export function SearchIcon(props: IconProps) {
  return <IconBase {...props}><circle cx="11" cy="11" r="6.5" /><path d="m16 16 4 4" /></IconBase>
}

export function SettingsIcon(props: IconProps) {
  return (
    <IconBase {...props}>
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-4V21a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9A1.7 1.7 0 0 0 3 14H2.8v-4H3a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1a1.7 1.7 0 0 0 1.9.3A1.7 1.7 0 0 0 10 3V2.8h4V3a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v4H21a1.7 1.7 0 0 0-1.6 1Z" />
    </IconBase>
  )
}

export function DeviceIcon(props: IconProps) {
  return <IconBase {...props}><rect x="4" y="3" width="16" height="13" rx="2" /><path d="M8 21h8M12 16v5" /></IconBase>
}

export function TerminalIcon(props: IconProps) {
  return <IconBase {...props}><rect x="3" y="4" width="18" height="16" rx="2" /><path d="m7 9 3 3-3 3M13 15h4" /></IconBase>
}

export function ActivityIcon(props: IconProps) {
  return <IconBase {...props}><path d="M3 12h4l2-6 4 12 2-6h6" /></IconBase>
}

export function PanelsIcon(props: IconProps) {
  return <IconBase {...props}><rect x="3" y="4" width="18" height="16" rx="2" /><path d="M15 4v16M15 10h6" /></IconBase>
}

export function MaximizeIcon(props: IconProps) {
  return <IconBase {...props}><path d="M8 3H3v5M16 3h5v5M8 21H3v-5M16 21h5v-5" /></IconBase>
}

export function RestoreIcon(props: IconProps) {
  return <IconBase {...props}><path d="M8 3v5H3M16 3v5h5M8 21v-5H3M16 21v-5h5" /></IconBase>
}

export function CloseIcon(props: IconProps) {
  return <IconBase {...props}><path d="m6 6 12 12M18 6 6 18" /></IconBase>
}

export function SendIcon(props: IconProps) {
  return <IconBase {...props}><path d="m5 12 14-7-4.5 14-3-5.5L5 12Z" /><path d="m11.5 13.5 4-4" /></IconBase>
}

export function SparklesIcon(props: IconProps) {
  return <IconBase {...props}><path d="m12 3 1.2 3.8L17 8l-3.8 1.2L12 13l-1.2-3.8L7 8l3.8-1.2L12 3ZM18.5 14l.7 2.3 2.3.7-2.3.7-.7 2.3-.7-2.3-2.3-.7 2.3-.7.7-2.3ZM5 13l.8 2.2L8 16l-2.2.8L5 19l-.8-2.2L2 16l2.2-.8L5 13Z" /></IconBase>
}

export function UserIcon(props: IconProps) {
  return <IconBase {...props}><circle cx="12" cy="8" r="3" /><path d="M5 21a7 7 0 0 1 14 0" /></IconBase>
}

export function ModelIcon(props: IconProps) {
  return <IconBase {...props}><path d="M12 3 4 7v10l8 4 8-4V7l-8-4Z" /><path d="m4 7 8 4 8-4M12 11v10" /></IconBase>
}

export function LogOutIcon(props: IconProps) {
  return <IconBase {...props}><path d="M10 5H5v14h5M14 8l4 4-4 4M18 12H9" /></IconBase>
}

export function TrashIcon(props: IconProps) {
  return <IconBase {...props}><path d="M4 7h16M9 7V4h6v3M7 7l1 13h8l1-13M10 11v5M14 11v5" /></IconBase>
}

export function ArchiveIcon(props: IconProps) {
  return <IconBase {...props}><path d="M4 7h16v13H4V7ZM3 3h18v4H3V3ZM9 11h6" /></IconBase>
}
