export interface AgentExperienceCapabilities {
  orderedConversation: boolean
  separateReasoning: boolean
  toolDisclosure: boolean
  approvalComposer: boolean
  sessionNavigation: boolean
  cancelTurn: boolean
  steerTurn: boolean
  queueMessages: boolean
  sessionMutations: boolean
}

export interface AgentExperienceDescriptor {
  id: 'dsh'
  label: string
  source: string
  sourceRevision: string
  capabilities: AgentExperienceCapabilities
}

export const dshAgentExperience: AgentExperienceDescriptor = Object.freeze({
  id: 'dsh',
  label: 'DSH',
  source: 'deepseek-ai/deepseek-harness',
  sourceRevision: '47f943859bef60e4160492346772ded9b24f765a',
  capabilities: Object.freeze({
    orderedConversation: true,
    separateReasoning: true,
    toolDisclosure: true,
    approvalComposer: true,
    sessionNavigation: true,
    cancelTurn: false,
    steerTurn: false,
    queueMessages: false,
    sessionMutations: false,
  }),
})

export function controllerRuntimeDescription(providerLabel: string): string {
  return `${dshAgentExperience.label} 体验层 · ${providerLabel} 运行时`
}
