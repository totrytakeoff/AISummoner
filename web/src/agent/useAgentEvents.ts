import { useEffect, useReducer, useRef, useState } from 'react'
import { agentEventsURL } from '../api/client'
import { agentEventNames } from '../api/types'
import type { AgentEventName, ToolDecision } from '../api/types'
import {
  addUserMessage,
  initialAgentViewState,
  markToolDecision,
  parseAgentEvent,
  projectAgentEvent,
} from './events'
import type { AgentViewState, ParsedAgentEvent } from './events'

export type AgentStreamState = 'idle' | 'connecting' | 'open' | 'error'

const streamDisconnectedMessage = 'Agent event stream disconnected. Waiting to reconnect…'

interface Submission {
  id: string
  previousView: AgentViewState
  sawTurnEvent: boolean
}

interface ManagedState {
  sessionID: string | null
  view: AgentViewState
  submission?: Submission
}

interface ConnectionState {
  sessionID: string | null
  status: AgentStreamState
}

interface SubmissionActivity {
  sessionID: string
  messageID: string
  sawTurnEvent: boolean
}

type Action =
  | { type: 'reset'; sessionID: string | null; view: AgentViewState }
  | { type: 'event'; sessionID: string; event: ParsedAgentEvent }
  | { type: 'begin_user'; sessionID: string; messageID: string; content: string }
  | { type: 'accept_user'; sessionID: string; messageID: string }
  | { type: 'reject_user'; sessionID: string; messageID: string }
  | { type: 'decision'; sessionID: string; toolID: string; decision: ToolDecision }
  | { type: 'stream_error'; sessionID: string }
  | { type: 'stream_open'; sessionID: string }

const initialManagedState: ManagedState = {
  sessionID: null,
  view: initialAgentViewState,
}

function forCurrentSession(state: ManagedState, sessionID: string): boolean {
  return state.sessionID === sessionID
}

function reducer(state: ManagedState, action: Action): ManagedState {
  if (action.type === 'reset') {
    return { sessionID: action.sessionID, view: action.view }
  }
  if (!forCurrentSession(state, action.sessionID)) return state

  switch (action.type) {
    case 'event':
      return {
        ...state,
        view: projectAgentEvent(state.view, action.event),
        submission: state.submission
          ? { ...state.submission, sawTurnEvent: state.submission.sawTurnEvent || action.event.type !== 'session.state' }
          : undefined,
      }
    case 'begin_user':
      if (state.submission) return state
      return {
        ...state,
        view: addUserMessage(state.view, action.content, action.messageID),
        submission: { id: action.messageID, previousView: state.view, sawTurnEvent: false },
      }
    case 'accept_user':
      return state.submission?.id === action.messageID ? { ...state, submission: undefined } : state
    case 'reject_user':
      if (state.submission?.id !== action.messageID) return state
      return {
        ...state,
        view: state.submission.sawTurnEvent ? state.view : state.submission.previousView,
        submission: undefined,
      }
    case 'decision':
      return { ...state, view: markToolDecision(state.view, action.toolID, action.decision) }
    case 'stream_error':
      return state.view.failure && state.view.failure !== streamDisconnectedMessage
        ? state
        : { ...state, view: { ...state.view, failure: streamDisconnectedMessage } }
    case 'stream_open':
      return state.view.failure === streamDisconnectedMessage
        ? { ...state, view: { ...state.view, failure: undefined } }
        : state
  }
}

export function useAgentEvents(sessionID: string | null, initialView: AgentViewState = initialAgentViewState) {
  const [managed, dispatch] = useReducer(reducer, initialManagedState)
  const [connection, setConnection] = useState<ConnectionState>({ sessionID: null, status: 'idle' })
  const messageSequence = useRef(0)
  const submissionActivity = useRef<SubmissionActivity | null>(null)
  const initialViewRef = useRef(initialView)
  initialViewRef.current = initialView

  useEffect(() => {
    submissionActivity.current = null
    dispatch({ type: 'reset', sessionID, view: initialViewRef.current })
    if (!sessionID) {
      setConnection({ sessionID: null, status: 'idle' })
      return
    }

    const currentSessionID = sessionID
    setConnection({ sessionID: currentSessionID, status: 'connecting' })
    const source = new EventSource(agentEventsURL(currentSessionID), { withCredentials: true })
    const listeners = new Map<AgentEventName, EventListener>()

    const opened: EventListener = () => {
      setConnection((current) => current.sessionID === currentSessionID
        ? { sessionID: currentSessionID, status: 'open' }
        : current)
      dispatch({ type: 'stream_open', sessionID: currentSessionID })
    }
    const failed: EventListener = () => {
      setConnection((current) => current.sessionID === currentSessionID
        ? { sessionID: currentSessionID, status: 'error' }
        : current)
      dispatch({ type: 'stream_error', sessionID: currentSessionID })
    }

    source.addEventListener('open', opened)
    source.addEventListener('error', failed)
    for (const name of agentEventNames) {
      const listener: EventListener = (rawEvent) => {
        const message = rawEvent as MessageEvent<string>
        const event = parseAgentEvent(name, message.data)
        if (event) {
          const submission = submissionActivity.current
          if (submission?.sessionID === currentSessionID && event.type !== 'session.state') {
            submission.sawTurnEvent = true
          }
          dispatch({ type: 'event', sessionID: currentSessionID, event })
        }
      }
      source.addEventListener(name, listener)
      listeners.set(name, listener)
    }

    return () => {
      source.removeEventListener('open', opened)
      source.removeEventListener('error', failed)
      for (const [name, listener] of listeners) source.removeEventListener(name, listener)
      source.close()
    }
  }, [sessionID])

  const state = managed.sessionID === sessionID ? managed.view : initialView
  const streamState: AgentStreamState = connection.sessionID === sessionID
    ? connection.status
    : sessionID ? 'connecting' : 'idle'

  return {
    state,
    streamState,
    beginUserMessage: (content: string): string | null => {
      if (!sessionID) return null
      const messageID = `user-${++messageSequence.current}`
      submissionActivity.current = { sessionID, messageID, sawTurnEvent: false }
      dispatch({ type: 'begin_user', sessionID, messageID, content })
      return messageID
    },
    acceptUserMessage: (messageID: string) => {
      if (!sessionID) return
      if (submissionActivity.current?.sessionID === sessionID && submissionActivity.current.messageID === messageID) {
        submissionActivity.current = null
      }
      dispatch({ type: 'accept_user', sessionID, messageID })
    },
    rejectUserMessage: (messageID: string): boolean => {
      if (!sessionID) return false
      const submission = submissionActivity.current
      const sawTurnEvent = submission?.sessionID === sessionID && submission.messageID === messageID
        ? submission.sawTurnEvent
        : false
      if (submission?.sessionID === sessionID && submission.messageID === messageID) {
        submissionActivity.current = null
      }
      dispatch({ type: 'reject_user', sessionID, messageID })
      return sawTurnEvent
    },
    markDecision: (toolID: string, decision: ToolDecision) => {
      if (sessionID) dispatch({ type: 'decision', sessionID, toolID, decision })
    },
  }
}
