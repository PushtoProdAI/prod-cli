// @ts-ignore - Deno Sentry module types
import * as Sentry from 'https://deno.land/x/sentry/index.mjs'

// Deno global declaration for Edge Functions
declare const Deno: {
  env: {
    get(key: string): string | undefined
  }
}

let initialized = false

/**
 * Initialize Sentry for Supabase Edge Functions
 */
export function initSentry(): void {
  if (initialized) {
    return
  }

  const dsn = Deno.env.get('SB_SENTRY_DSN')
  if (!dsn) {
    console.warn('SENTRY_DSN not configured, skipping Sentry initialization')
    return
  }

  // @ts-ignore - Sentry types
  Sentry.init({
    dsn,
    // Disable default integrations to avoid scope sharing between requests
    defaultIntegrations: false,
    // Performance Monitoring
    tracesSampleRate: 0.1,
    // Set sampling rate for profiling - this is relative to tracesSampleRate
    profilesSampleRate: 0.1,
    // Disable sending personally identifiable information by default
    sendDefaultPii: false,
    // Remove server_name and geographic data for privacy
    beforeSend: (event: any) => {
      event.server_name = ''
      // Remove user IP address and any geographic information
      if (event.user && event.user.ip_address) {
        event.user.ip_address = ''
      }
      return event
    },
  })

  // Set region and execution_id as custom tags
  const region = Deno.env.get('SB_REGION')
  const executionId = Deno.env.get('SB_EXECUTION_ID')
  
  if (region) {
    // @ts-ignore - Sentry types
    Sentry.setTag('region', region)
  }
  if (executionId) {
    // @ts-ignore - Sentry types
    Sentry.setTag('execution_id', executionId)
  }

  initialized = true
}

/**
 * Capture an exception with Sentry using isolated scope
 * This prevents context sharing between requests
 */
export function captureException(error: Error, context?: Record<string, any>): void {
  if (!initialized) {
    console.error('Sentry not initialized, logging error:', error)
    return
  }

  // @ts-ignore - Sentry types
  Sentry.withScope((scope: any) => {
    // Add context data if provided
    if (context) {
      for (const [key, value] of Object.entries(context)) {
        scope.setContext(key, typeof value === 'object' ? value : { value })
      }
    }

    // Add function-specific tags
    scope.setTag('function_name', Deno.env.get('SUPABASE_FUNCTION_NAME') || 'unknown')
    scope.setTag('environment', Deno.env.get('ENVIRONMENT') || 'production')

    // @ts-ignore - Sentry types
    Sentry.captureException(error)
  })
}

/**
 * Capture a message with Sentry using isolated scope
 */
export function captureMessage(
  message: string, 
  level: 'fatal' | 'error' | 'warning' | 'info' | 'debug' = 'info', 
  context?: Record<string, any>
): void {
  if (!initialized) {
    console.log('Sentry not initialized, logging message:', message)
    return
  }

  // @ts-ignore - Sentry types
  Sentry.withScope((scope: any) => {
    // Add context data if provided
    if (context) {
      for (const [key, value] of Object.entries(context)) {
        scope.setContext(key, typeof value === 'object' ? value : { value })
      }
    }

    // Add function-specific tags
    scope.setTag('function_name', Deno.env.get('SUPABASE_FUNCTION_NAME') || 'unknown')
    scope.setTag('environment', Deno.env.get('ENVIRONMENT') || 'production')

    scope.setLevel(level)
    // @ts-ignore - Sentry types
    Sentry.captureMessage(message)
  })
}

/**
 * Flush pending Sentry events
 * Should be called before function completes
 */
export async function flushSentry(timeout = 2000): Promise<void> {
  if (!initialized) {
    return
  }

  // @ts-ignore - Sentry types
  await Sentry.flush(timeout)
}

/**
 * Add a breadcrumb for debugging
 */
export function addBreadcrumb(
  message: string, 
  category = 'custom', 
  level: 'fatal' | 'error' | 'warning' | 'info' | 'debug' = 'info'
): void {
  if (!initialized) {
    return
  }

  // @ts-ignore - Sentry types
  Sentry.addBreadcrumb({
    message,
    category,
    level,
    timestamp: Date.now() / 1000,
  })
}
