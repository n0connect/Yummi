import { t } from '../i18n/strings'

const THINK_BLOCK = /<think\b[^>]*>[\s\S]*?<\/think\s*>/gi
const UNCLOSED_THINK = /<think\b[^>]*>[\s\S]*$/gi
const INTERNAL_CONTROL = /^\s*\[(RUN_COMMAND|UPDATE_PLAN|FINISH|HANDOFF|REMEMBER|ASK_OPERATOR|SEND_FULL|CONTINUE|ABORT|STOP_TASK)\]\s*$/im

const MAX_VISIBLE_CHARS = 64 * 1024

/**
 * Final client-side trust boundary for model-authored text. The server applies
 * the same policy to new responses; this layer also protects sessions saved by
 * older versions and defensive rendering during rolling upgrades.
 */
export function sanitizeOperatorText(value: string, fallback = t('operatorText.fallback')): string {
  if (INTERNAL_CONTROL.test(value)) return fallback

  const clean = value
    .replace(THINK_BLOCK, '')
    .replace(UNCLOSED_THINK, '')
    .trim()

  if (!clean) return fallback
  if (clean.length <= MAX_VISIBLE_CHARS) return clean

  const edge = MAX_VISIBLE_CHARS / 2
  return `${clean.slice(0, edge)}\n…\n${clean.slice(-edge)}`
}
