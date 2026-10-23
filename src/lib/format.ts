import { format, formatDistanceToNowStrict } from 'date-fns'

export function relativeTime(date: Date): string {
  return formatDistanceToNowStrict(date, { addSuffix: true })
}

/** Numeric date + 12-hour time for the "When" column, e.g. `10/21/2026 @ 5:55 pm`. */
export function whenTimestamp(date: Date): string {
  return `${format(date, 'M/d/yyyy')} @ ${format(date, 'h:mm a').toLowerCase()}`
}

export function fullTimestamp(date: Date): string {
  return format(date, 'MMM d, yyyy · HH:mm')
}
