export type NodeStatus = "up" | "down" | "disabled" | ""

export interface NodeView {
  uuid: string
  name: string
  address: string
  countryCode: string
  status: NodeStatus
  excluded: boolean
  downSince: string | null
  upSince: string | null
  lastMessage: string
  hosts: string[]
  traffic: NodeTraffic
  /** null while the node is not up or the panel does not report it. */
  online: number | null
  block: NodeBlock
}

/** Online drop on a node that stays connected: a suspected block. */
export interface NodeBlock {
  /** Frozen at the drop while there is one, otherwise the median of the last hour; 0 without history. */
  usual: number
  dropSince?: string
  blockedSince?: string
}

export interface NodeTraffic {
  usedBytes: number
  /** 0 when the node has no limit or traffic tracking is off in the panel. */
  limitBytes: number
  resetDay?: number | null
  overLimit: boolean
}

export type DisableReason = "nodes down" | "traffic" | "blocked"

export type HostState =
  | "ok"
  | "at_risk"
  | "managed"
  | "dry_run"
  | "overridden"
  | "disabled"
  | "excluded"
  | "unbound"

export interface HostView {
  uuid: string
  remark: string
  address: string
  port: number
  isDisabled: boolean
  isHidden: boolean
  nodes: string[]
  tags: string[]
  excluded: boolean
  state: HostState
  reason?: DisableReason
  disableAt?: string
}

export interface TemplatePatch {
  templateUuid: string
  templateName: string
  patch: {
    groups?: { group: string }[]
    rules?: { rule: string }[]
  }
}

export interface ManagedHost {
  uuid: string
  remark: string
  nodes: string[]
  state: "disabled" | "dry_run" | "overridden"
  reason: DisableReason
  disabledAt: string
  mihomo: TemplatePatch[] | null
}

export interface Stats {
  nodesTotal: number
  nodesUp: number
  nodesDown: number
  nodesDisabled: number
  hostsTotal: number
  hostsBound: number
  hostsManaged: number
  hostsAtRisk: number
  nodesOverLimit: number
  nodesBlocked: number
}

export interface Snapshot {
  checkedAt: string
  nextCheckAt: string
  panelOk: boolean
  panelError?: string
  panelErrorCode?: string
  panelVersion?: string
  nodes: NodeView[] | null
  hosts: HostView[] | null
  managed: ManagedHost[] | null
  stats: Stats
}

export interface StatusResponse {
  version: string
  snapshot: Snapshot
  checker: {
    enabled: boolean
    dryRun: boolean
    policy: "all" | "any"
    downMinutes: number
    recoveryMinutes: number
    intervalSeconds: number
    autoEnable: boolean
    traffic: boolean
    trafficPercent: number
    block: boolean
    blockHosts: boolean
    blockMinutes: number
    mihomo: boolean
    telegram: boolean
    configured: boolean
  }
}

export interface TelegramEvent {
  enabled: boolean
  template: string
}

export interface Settings {
  panel: {
    url: string
    token?: string
    headers: Record<string, string>
    insecureTls: boolean
  }
  checker: {
    enabled: boolean
    intervalSeconds: number
    downMinutes: number
    recoveryMinutes: number
    policy: "all" | "any"
    autoEnable: boolean
    treatDisabledNodeAsDown: boolean
    dryRun: boolean
    massDownGuardPercent: number
    excludedNodes: string[]
    excludedHosts: string[]
  }
  traffic: {
    enabled: boolean
    disablePercent: number
  }
  block: {
    enabled: boolean
    dropPercent: number
    minUsers: number
    confirmMinutes: number
    guardPercent: number
    disableHosts: boolean
    disableMinutes: number
  }
  mihomo: {
    enabled: boolean
    templateTypes: string[]
    templateUuids: string[]
    cleanRules: boolean
    emptyGroupFallback: string
  }
  telegram: {
    enabled: boolean
    botToken?: string
    chatIds: string[]
    threadId: string
    apiUrl: string
    proxy: string
    silent: boolean
    events: Record<string, TelegramEvent>
  }
  general: {
    eventRetentionDays: number
  }
}

export interface SettingsResponse {
  settings: Settings
  secrets: { panelToken: boolean; botToken: boolean }
  eventTypes: string[]
  defaultTemplates: Record<string, string>
  templateTypes: string[]
}

export interface AppEvent {
  id: number
  time: string
  level: "info" | "warn" | "error"
  type: string
  message: string
  nodeUuid?: string
  hostUuid?: string
  meta?: Record<string, unknown>
}

export interface PanelInfo {
  version: string
  nodes: number
  hosts: number
  templates: number
  templatesError?: string
}

export interface SubscriptionTemplate {
  uuid: string
  name: string
  templateType: string
}

export interface TemplateRef {
  uuid: string
  name: string
  type: string
  groups: string[] | null
  rules: string[] | null
  error?: string
}

export interface AuthState {
  setupRequired: boolean
  authenticated: boolean
  username?: string
  version: string
}

export interface BuildInfo {
  version: string
  commit: string
  branch: string
  buildTime: string
  repository: string
  goVersion: string
  platform: string
  startedAt: string
}

export interface UpdateStatus {
  enabled: boolean
  channel: "release" | "branch" | "none"
  available: boolean
  latest?: string
  url?: string
  checkedAt?: string
  error?: string
}

export interface AboutResponse {
  build: BuildInfo
  update: UpdateStatus
  panelVersion: string
}
