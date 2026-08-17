import type { GatewayStatus } from '../../entities/types'
import type { EventConnectionState } from '../chat/useChatStream'

export type TrustState = 'trusted' | 'recovering' | 'attention' | 'offline'

export function deriveTrustState(
  events: EventConnectionState,
  gateway: Pick<GatewayStatus, 'status' | 'rules_active' | 'policy_kernel_consistent'> | null,
): TrustState {
  if (!gateway || gateway.status !== 'connected') return 'offline'
  if (!gateway.rules_active || !gateway.policy_kernel_consistent) return 'attention'
  if (events !== 'live') return 'recovering'
  return 'trusted'
}
