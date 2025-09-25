/// <reference types="https://esm.sh/@supabase/functions-js/src/edge-runtime.d.ts" />
import { serve } from "https://deno.land/std@0.168.0/http/server.ts"
import { createClient } from 'https://esm.sh/@supabase/supabase-js@2'
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts'

// Initialize Sentry
initSentry()

// Get auth URL from environment variable based on environment
const getAuthURL = () => {
  const environment = Deno.env.get('ENVIRONMENT') || 'staging' // Default to staging for local development
  
  if (environment === 'local') {
    return Deno.env.get('LOCAL_AUTH_URL') || 'http://localhost:5175/cli-auth'
  }
  
  if (environment === 'staging') {
    return Deno.env.get('STAGING_AUTH_URL') || 'https://staging--prodai-landing.netlify.app/cli-auth'
  }
  
  return Deno.env.get('PRODUCTION_AUTH_URL') || 'https://pushtoprod.ai/cli-auth'
}

const AUTH_URL = getAuthURL()

const corsHeaders = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Allow-Headers': 'authorization, x-client-info, apikey, content-type',
  'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
  'Access-Control-Max-Age': '86400',
}

serve(async (req) => {
  try {
    // Handle CORS
    if (req.method === 'OPTIONS') {
      return new Response('ok', { headers: corsHeaders })
    }

  const url = new URL(req.url)
  
  // Handle GET request - serve the CLI auth page
         if (req.method === 'GET') {
           const state = url.searchParams.get('state')
           const code = url.searchParams.get('code')
           const error = url.searchParams.get('error')
           const callbackUrl = url.searchParams.get('callback_url')
           
           // If we have an error, redirect to error page
           if (error) {
             const errorDescription = url.searchParams.get('error_description') || error
             if (callbackUrl) {
               const errorURL = `${callbackUrl}?error=${encodeURIComponent(error)}&error_description=${encodeURIComponent(errorDescription)}`
               return Response.redirect(errorURL, 302)
             } else {
               const errorURL = `${AUTH_URL}?error=${encodeURIComponent(error)}&error_description=${encodeURIComponent(errorDescription)}`
               return Response.redirect(errorURL, 302)
             }
           }
           
           // If we have a code, exchange it for a token and show success
           if (code && state) {
             try {
               const token = await exchangeCodeForToken(code, state)
               if (callbackUrl) {
                 const successURL = `${callbackUrl}?token=${encodeURIComponent(token)}`
                 return Response.redirect(successURL, 302)
               } else {
                 const successURL = `${AUTH_URL}?success=true&token=${encodeURIComponent(token)}`
                 return Response.redirect(successURL, 302)
               }
              } catch (error) {
                captureException(error, {
                  function: 'cli-auth',
                  operation: 'token_exchange',
                  method: 'GET',
                  has_callback_url: !!callbackUrl
                })
                if (callbackUrl) {
                  const errorURL = `${callbackUrl}?error=token_exchange_failed&error_description=${encodeURIComponent(error.message)}`
                  return Response.redirect(errorURL, 302)
                } else {
                  const errorURL = `${AUTH_URL}?error=token_exchange_failed&error_description=${encodeURIComponent(error.message)}`
                  return Response.redirect(errorURL, 302)
                }
              }
           }
           
           // Default: redirect to hosted auth page
           const authPageURL = `${AUTH_URL}?state=${encodeURIComponent(state || '')}${callbackUrl ? `&callback_url=${encodeURIComponent(callbackUrl)}` : ''}`
           return Response.redirect(authPageURL, 302)
         }
  
  // Handle POST request - generate auth URL for CLI or handle email/password auth
  if (req.method === 'POST') {
    try {
      const body = await req.json()
      
             // If we have an access_token, this is email/password authentication
             if (body.access_token && body.state) {
               try {
                 // Verify the access token and get user info
                 const supabaseUrl = Deno.env.get('SUPABASE_URL') || ''
                 const supabaseAnonKey = Deno.env.get('SUPABASE_ANON_KEY') || ''
                 
                 const supabase = createClient(supabaseUrl, supabaseAnonKey, {
                   auth: {
                     autoRefreshToken: false,
                     persistSession: false
                   }
                 })
                 
                 // Set the session with the access token
                 const { data: { user }, error: userError } = await supabase.auth.getUser(body.access_token)
                 
                 if (userError || !user) {
                   throw new Error('Invalid access token')
                 }
                 
                 // Generate a CLI token (you can customize this logic)
                 const cliToken = generateCLIToken(user.id, body.access_token)
                 
                 // If callback_url is provided, return JSON with redirect URL
                 if (body.callback_url) {
                   const successURL = `${body.callback_url}?token=${encodeURIComponent(cliToken)}`
                   return new Response(
                     JSON.stringify({ 
                       success: true,
                       token: cliToken,
                       user_id: user.id,
                       redirect_url: successURL
                     }),
                     { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
                   )
                 }
                 
                 return new Response(
                   JSON.stringify({ 
                     success: true,
                     token: cliToken,
                     user_id: user.id 
                   }),
                   { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
                 )
                 
                } catch (error) {
                  console.error('Email auth error:', error)
                  captureException(error, {
                    function: 'cli-auth',
                    operation: 'email_auth',
                    method: 'POST',
                    has_callback_url: !!body.callback_url
                  })
                 
                 // If callback_url is provided, return JSON with error redirect URL
                 if (body.callback_url) {
                   const errorURL = `${body.callback_url}?error=authentication_failed&error_description=${encodeURIComponent(error.message)}`
                   return new Response(
                     JSON.stringify({ 
                       success: false,
                       error: 'Authentication failed',
                       error_description: error.message,
                       redirect_url: errorURL
                     }),
                     { status: 401, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
                   )
                 }
                 
                 return new Response(
                   JSON.stringify({ error: 'Authentication failed' }),
                   { status: 401, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
                 )
               }
             }
      
      // Otherwise, generate OAuth URL
      const { state } = body
      
      if (!state) {
        return new Response(
          JSON.stringify({ error: 'Missing state parameter' }),
          { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }
      
      // Generate the OAuth URL
      const authURL = generateAuthURL(state)
      
      return new Response(
        JSON.stringify({ auth_url: authURL }),
        { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
      
    } catch (error) {
      console.error('Auth URL generation error:', error)
      captureException(error, {
        function: 'cli-auth',
        operation: 'auth_url_generation',
        method: 'POST'
      })
      return new Response(
        JSON.stringify({ error: 'Failed to generate auth URL' }),
        { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }
  }
  
  return new Response('Method not allowed', { status: 405 })
  
  } catch (error) {
    console.error('Unexpected error in cli-auth function:', error)
    captureException(error, {
      function: 'cli-auth',
      method: req.method,
      url: req.url
    })
    await flushSentry()
    
    return new Response(
      JSON.stringify({ error: 'Internal server error' }),
      { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }
})

function generateAuthURL(state: string): string {
  const supabaseUrl = Deno.env.get('SUPABASE_URL') || ''
  const redirectUrl = `${supabaseUrl}/functions/v1/cli-auth`
  
  const params = new URLSearchParams({
    provider: 'github', // or 'google', 'discord', etc.
    redirect_to: redirectUrl,
    state: state
  })
  
  return `${supabaseUrl}/auth/v1/authorize?${params.toString()}`
}

async function exchangeCodeForToken(code: string, state: string): Promise<string> {
  const supabaseUrl = Deno.env.get('SUPABASE_URL') || ''
  const supabaseAnonKey = Deno.env.get('SUPABASE_ANON_KEY') || ''
  
  const supabase = createClient(supabaseUrl, supabaseAnonKey, {
    auth: {
      autoRefreshToken: false,
      persistSession: false
    }
  })
  
  // Exchange the code for a session
  const { data, error } = await supabase.auth.exchangeCodeForSession(code)
  
  if (error) {
    throw new Error(`Failed to exchange code: ${error.message}`)
  }
  
  if (!data.session) {
    throw new Error('No session returned from code exchange')
  }
  
  // Generate a CLI-specific token (you might want to store this in a database)
  const cliToken = generateCLIToken(data.session.user.id, data.session.access_token)
  
  return cliToken
}

function generateCLIToken(userId: string, accessToken: string): string {
  // In a real implementation, you'd want to:
  // 1. Store this token in your database with expiration
  // 2. Use a proper JWT or similar
  // 3. Include user permissions/scopes
  
  const tokenData = {
    user_id: userId,
    access_token: accessToken,
    issued_at: Date.now(),
    expires_at: Date.now() + (30 * 24 * 60 * 60 * 1000) // 30 days
  }
  
  // For now, just base64 encode (in production, use proper JWT)
  return btoa(JSON.stringify(tokenData))
}


