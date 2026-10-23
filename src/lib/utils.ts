import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}

const LANE_COLORS = [
  'var(--color-lane-0)',
  'var(--color-lane-1)',
  'var(--color-lane-2)',
  'var(--color-lane-3)',
  'var(--color-lane-4)',
  'var(--color-lane-5)',
  'var(--color-lane-6)',
  'var(--color-lane-7)',
] as const

export function laneColor(lane: number): string {
  return LANE_COLORS[((lane % LANE_COLORS.length) + LANE_COLORS.length) % LANE_COLORS.length]
}

/** Stable FNV-1a hue index for a label, so a stack of chips for different
 * branches on the same graph lane (which share one lane color) still reads
 * as one color per branch. */
export function chipHueIndex(label: string): number {
  let hash = 2166136261
  for (let i = 0; i < label.length; i++) {
    hash ^= label.charCodeAt(i)
    hash = Math.imul(hash, 16777619)
  }
  return (hash >>> 0) % LANE_COLORS.length
}

/** Stable per-label color: the hue derives from the label alone, so a
 * branch keeps its color wherever its chip is shown and across checkouts
 * (lane colors re-map when the head moves). Tags override this inside the
 * chip itself. */
export function labelLaneColor(label: string): string {
  return LANE_COLORS[chipHueIndex(label)]
}

/** Per-label lane colors. Each label keeps its stable hue, but a hash
 * collision with a hue already picked earlier in the list is nudged to the
 * next free hue, so sibling chips never render in the same color. */
export function distinctLaneColors(labels: string[]): string[] {
  const picked: string[] = []
  return labels.map((label) => {
    let index = chipHueIndex(label)
    let color = LANE_COLORS[index]
    while (picked.includes(color)) {
      index = (index + 1) % LANE_COLORS.length
      color = LANE_COLORS[index]
    }
    picked.push(color)
    return color
  })
}
