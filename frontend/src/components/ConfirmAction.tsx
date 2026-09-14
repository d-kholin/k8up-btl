import { useState } from 'react'
import { Alert } from './ui/alert'
import { Button } from './ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from './ui/dialog'

export default function ConfirmAction({
  open,
  onClose,
  title,
  description,
  action,
  onConfirm,
}: {
  open: boolean
  onClose: () => void
  title: string
  description: string
  action: string
  onConfirm: () => Promise<unknown>
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (!value && !busy) {
          setError('')
          onClose()
        }
      }}
    >
      <DialogContent
        onEscapeKeyDown={(e) => {
          if (busy) e.preventDefault()
        }}
        onPointerDownOutside={(e) => {
          if (busy) e.preventDefault()
        }}
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {error && <Alert variant="danger">{error}</Alert>}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Keep running
          </Button>
          <Button
            variant="destructive"
            disabled={busy}
            onClick={async () => {
              setBusy(true)
              setError('')
              try {
                await onConfirm()
                onClose()
              } catch (e) {
                setError((e as Error).message)
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? 'Working…' : action}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
