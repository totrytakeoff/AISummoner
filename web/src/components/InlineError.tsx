export function InlineError({ message }: { message: string | null }) {
  if (!message) return null
  return (
    <div className="notice error" role="alert">
      {message}
    </div>
  )
}
