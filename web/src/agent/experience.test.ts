import { controllerRuntimeDescription, dshAgentExperience } from './experience'

describe('DSH-first Agent experience descriptor', () => {
  it('keeps presentation identity separate from the actual runtime', () => {
    expect(dshAgentExperience).toMatchObject({
      id: 'dsh',
      sourceRevision: '47f943859bef60e4160492346772ded9b24f765a',
      capabilities: {
        orderedConversation: true,
        separateReasoning: true,
        approvalComposer: true,
        cancelTurn: false,
        steerTurn: false,
      },
    })
    expect(controllerRuntimeDescription('OpenCode')).toBe('DSH 体验层 · OpenCode 运行时')
    expect(controllerRuntimeDescription('DeepSeek')).toBe('DSH 体验层 · DeepSeek 运行时')
  })
})
