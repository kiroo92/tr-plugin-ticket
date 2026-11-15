export interface Ticket {
  id: number
  user_id: number
  username: string
  title: string
  category: string
  priority: string
  status: 'open' | 'resolved'
  awaiting_admin: boolean
  created_at: number
  updated_at: number
}
export interface Message { id: number; author: string; role: string; body: string; created_at: number }
export interface Detail { ticket: Ticket; messages: Message[]; next_before: number }
export interface TicketList { rows: Ticket[]; next_offset: number; counts: Record<string, number> }
export interface NoticeStatus {
  enabled: boolean
  configured: boolean
  pending: number
  failed: number
  recent: { id: number; ticket_id: number | null; state: string; attempts: number; created_at: number; last_error: string }[]
}
export interface Pending { path: string; body: Record<string, string>; label: string }
