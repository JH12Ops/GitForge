import { describe, expect, it } from 'vitest'
import { currentBackend } from './backend'

describe('backend detector', () => {
  it('reports no native backend inside vitest', () => {
    expect(currentBackend()).toBe('web')
  })
})
