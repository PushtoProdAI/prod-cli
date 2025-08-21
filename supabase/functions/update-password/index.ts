import { serve } from "https://deno.land/std@0.168.0/http/server.ts"
import { createClient } from 'https://esm.sh/@supabase/supabase-js@2'

const corsHeaders = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Allow-Headers': 'authorization, x-client-info, apikey, content-type',
}

serve(async (req) => {
  // Handle CORS
  if (req.method === 'OPTIONS') {
    return new Response('ok', { headers: corsHeaders })
  }

  const url = new URL(req.url)
  
  // Handle GET request - serve the password update form
  if (req.method === 'GET') {
    // The recovery token can come in different formats from Supabase
    // Could be in query params, hash, or various parameter names
    const html = getUpdatePasswordHTML()
    return new Response(html, {
      headers: { ...corsHeaders, 'Content-Type': 'text/html' }
    })
  }
  
  // Handle POST request - update the password using the recovery token
  if (req.method === 'POST') {
    try {
      const { password, token } = await req.json()
      
      if (!password || !token) {
        return new Response(
          JSON.stringify({ error: 'Missing password or token' }),
          { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }
      
      // Initialize Supabase client
      const supabaseUrl = Deno.env.get('SUPABASE_URL') || ''
      const supabaseAnonKey = Deno.env.get('SUPABASE_ANON_KEY') || ''
      
      const supabase = createClient(supabaseUrl, supabaseAnonKey, {
        auth: {
          autoRefreshToken: false,
          persistSession: false
        }
      })
      
      // First, exchange the recovery token for a session
      const { data: sessionData, error: sessionError } = await supabase.auth.verifyOtp({
        token_hash: token,
        type: 'recovery'
      })
      
      if (sessionError) {
        // Try alternate method - sometimes the token is directly usable
        const { data: user, error: updateError } = await supabase.auth.updateUser(
          { password },
          { 
            // Use the token as a bearer token
            headers: {
              Authorization: `Bearer ${token}`
            }
          }
        )
        
        if (updateError) {
          console.error('Password update error:', updateError)
          return new Response(
            JSON.stringify({ error: 'Invalid or expired token. Please request a new password reset.' }),
            { status: 401, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }
        
        return new Response(
          JSON.stringify({ success: true }),
          { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }
      
      // If we got a session, use it to update the password
      if (sessionData.session) {
        supabase.auth.setSession(sessionData.session)
        
        const { error: updateError } = await supabase.auth.updateUser({ password })
        
        if (updateError) {
          console.error('Password update error:', updateError)
          return new Response(
            JSON.stringify({ error: updateError.message }),
            { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }
        
        return new Response(
          JSON.stringify({ success: true }),
          { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }
      
      return new Response(
        JSON.stringify({ error: 'Failed to verify recovery token' }),
        { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
      
    } catch (error) {
      console.error('Password update error:', error)
      return new Response(
        JSON.stringify({ error: 'Failed to update password' }),
        { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }
  }
  
  return new Response('Method not allowed', { status: 405 })
})

function getUpdatePasswordHTML(): string {
  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Update Password - Prod CLI</title>
  <script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-black min-h-screen flex items-center justify-center p-5">
  <div class="bg-zinc-900 rounded-xl border border-zinc-800 p-8 w-full max-w-md">
    <div class="text-center mb-8">
      <h1 class="text-2xl font-semibold text-white mb-2">🔐 Set New Password</h1>
      <p class="text-sm text-zinc-400">Choose a strong password for your account</p>
    </div>
    
    <div id="errorMessage" class="bg-red-950/50 text-red-400 border border-red-900 p-3 rounded-md mb-5 text-sm hidden"></div>
    
    <form id="updateForm" onsubmit="updatePassword(event)" class="space-y-5">
      <div>
        <label for="password" class="block text-sm font-medium text-zinc-300 mb-2">New Password</label>
        <input type="password" id="password" name="password" required placeholder="••••••••" minlength="8"
          class="w-full px-3 py-2 bg-zinc-800 border border-zinc-700 text-white placeholder-zinc-500 rounded-lg focus:outline-none focus:ring-2 focus:ring-[#05B55E] focus:border-transparent">
        <div class="mt-2 text-xs text-zinc-500">
          <div id="lengthReq" class="flex items-center gap-1">
            <span class="icon">○</span>
            <span>At least 8 characters</span>
          </div>
        </div>
      </div>
      
      <div>
        <label for="confirmPassword" class="block text-sm font-medium text-zinc-300 mb-2">Confirm New Password</label>
        <input type="password" id="confirmPassword" name="confirmPassword" required placeholder="••••••••"
          class="w-full px-3 py-2 bg-zinc-800 border border-zinc-700 text-white placeholder-zinc-500 rounded-lg focus:outline-none focus:ring-2 focus:ring-[#05B55E] focus:border-transparent">
      </div>
      
      <button type="submit" id="submitBtn"
        class="w-full bg-[#05B55E] hover:bg-[#049a4e] disabled:bg-zinc-700 disabled:text-zinc-500 text-white font-medium py-3 px-4 rounded-lg transition-all disabled:cursor-not-allowed">
        Update Password
      </button>
    </form>
    
    <div id="loading" class="hidden text-center text-zinc-400 text-sm my-4">
      <svg class="animate-spin inline-block w-4 h-4 mr-2 text-[#05B55E]" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
        <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
      </svg>
      Updating password...
    </div>
  </div>

  <script type="module">
    // Import Supabase client for client-side token extraction if needed
    import { createClient } from 'https://cdn.jsdelivr.net/npm/@supabase/supabase-js@2/+esm'
    
    let recoveryToken = null
    
    // Try to extract token from URL (could be in hash or query params)
    function extractToken() {
      // Check hash fragment first (most common for Supabase)
      const hash = window.location.hash.substring(1)
      if (hash) {
        const hashParams = new URLSearchParams(hash)
        recoveryToken = hashParams.get('access_token') || 
                       hashParams.get('token') || 
                       hashParams.get('recovery_token')
        
        // Also check for error in hash
        const error = hashParams.get('error')
        if (error) {
          showError(hashParams.get('error_description') || error)
          document.getElementById('updateForm').style.display = 'none'
          return
        }
      }
      
      // Check query params as fallback
      if (!recoveryToken) {
        const searchParams = new URLSearchParams(window.location.search)
        recoveryToken = searchParams.get('access_token') || 
                       searchParams.get('token') || 
                       searchParams.get('recovery_token') ||
                       searchParams.get('code')
        
        // Check for error in query
        const error = searchParams.get('error')
        if (error) {
          showError(searchParams.get('error_description') || error)
          document.getElementById('updateForm').style.display = 'none'
          return
        }
      }
      
      // Log for debugging
      console.log('Token extraction:', {
        found: recoveryToken ? 'Yes' : 'No',
        source: hash ? 'hash' : 'query',
        url: window.location.href
      })
      
      if (!recoveryToken) {
        showError('No recovery token found. This link may be invalid or expired. Please request a new password reset.')
        document.getElementById('updateForm').style.display = 'none'
      }
    }
    
    // Extract token on page load
    extractToken()
    
    // Password validation
    document.getElementById('password').addEventListener('input', function(e) {
      const password = e.target.value
      const lengthReq = document.getElementById('lengthReq')
      
      if (password.length >= 8) {
        lengthReq.classList.add('text-[#05B55E]')
        lengthReq.classList.remove('text-zinc-500')
        lengthReq.querySelector('.icon').textContent = '✓'
      } else {
        lengthReq.classList.remove('text-[#05B55E]')
        lengthReq.classList.add('text-zinc-500')
        lengthReq.querySelector('.icon').textContent = '○'
      }
    })
    
    window.updatePassword = async function(event) {
      event.preventDefault()
      
      if (!recoveryToken) {
        showError('No recovery token found. Please request a new password reset.')
        return
      }
      
      const password = document.getElementById('password').value
      const confirmPassword = document.getElementById('confirmPassword').value
      
      // Validate passwords match
      if (password !== confirmPassword) {
        showError('Passwords do not match')
        return
      }
      
      // Validate password length
      if (password.length < 8) {
        showError('Password must be at least 8 characters')
        return
      }
      
      setLoading(true)
      hideMessages()
      
      try {
        const response = await fetch(window.location.origin + window.location.pathname, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({
            password: password,
            token: recoveryToken
          })
        })
        
        const data = await response.json()
        
        if (!response.ok) {
          throw new Error(data.error || 'Failed to update password')
        }
        
        // Show success
        showSuccess()
        
      } catch (error) {
        showError(error.message)
        setLoading(false)
      }
    }
    
    function setLoading(loading) {
      document.getElementById('loading').classList.toggle('hidden', !loading)
      document.getElementById('password').disabled = loading
      document.getElementById('confirmPassword').disabled = loading
      document.getElementById('submitBtn').disabled = loading
    }
    
    function showError(message) {
      const errorEl = document.getElementById('errorMessage')
      errorEl.textContent = message
      errorEl.classList.remove('hidden')
    }
    
    function hideMessages() {
      document.getElementById('errorMessage').classList.add('hidden')
    }
    
    function showSuccess() {
      // Replace entire form with success message
      document.querySelector('.bg-zinc-900').innerHTML = \`
        <div class="text-center">
          <div class="mb-4">
            <svg class="w-16 h-16 text-[#05B55E] mx-auto" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"></path>
            </svg>
          </div>
          <h2 class="text-2xl font-semibold text-white mb-2">Password Updated!</h2>
          <p class="text-zinc-400 mb-6">Your password has been successfully updated. You can now sign in with your new password.</p>
          <p class="text-sm text-zinc-500 mb-6">You can close this window and return to the CLI to sign in.</p>
        </div>
      \`
    }
  </script>
</body>
</html>`
}