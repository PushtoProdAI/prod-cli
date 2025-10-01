import { serve } from "https://deno.land/std@0.168.0/http/server.ts"
import { createClient } from 'https://esm.sh/@supabase/supabase-js@2'

// Types for LLM proxy requests and responses
interface LLMProxyRequest {
  function: string
  args: Record<string, any>
  model_preference?: string
  fallback_enabled?: boolean
  user_id?: string
}

interface LLMProxyResponse {
  success: boolean
  result?: any
  error?: {
    code: string
    message: string
    retry_after?: number
  }
  metadata?: {
    model_used: string
    tokens_used: number
    cost: number
    response_time_ms: number
  }
  fallback_available?: boolean
}

interface LLMProvider {
  name: string
  call: (prompt: string, model: string) => Promise<any>
  getCost: (tokens: number, model: string) => number
}

// OpenAI provider
class OpenAIProvider implements LLMProvider {
  name = 'openai'
  private apiKey: string

  constructor(apiKey: string) {
    this.apiKey = apiKey
  }

  async call(prompt: string, model: string): Promise<any> {
    
    const response = await fetch('https://api.openai.com/v1/chat/completions', {
      method: 'POST',
      headers: {
        'Authorization': `Bearer ${this.apiKey}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        model: model,
        messages: [
          { role: 'system', content: 'You are a helpful assistant that responds with valid JSON only.' },
          { role: 'user', content: prompt }
        ],
        temperature: 0.1,
        max_tokens: 2000,
      }),
    })

    
    if (!response.ok) {
      const errorText = await response.text()
      console.error(`OpenAI API error: ${errorText}`)
      
      throw new Error(`OpenAI API error: ${response.status} ${response.statusText} - ${errorText}`)
    }

    const data = await response.json()
    const result = data.choices[0].message.content
    
    return result
  }

  getCost(tokens: number, model: string): number {
    const costs: Record<string, number> = {
      'gpt-3.5-turbo': 0.0005 / 1000,
      'gpt-4o-mini': 0.00015 / 1000,
      'gpt-4o': 0.005 / 1000,
    }
    return tokens * (costs[model] || 0.0005 / 1000)
  }
}

// Anthropic provider
class AnthropicProvider implements LLMProvider {
  name = 'anthropic'
  private apiKey: string

  constructor(apiKey: string) {
    this.apiKey = apiKey
  }

  async call(prompt: string, model: string): Promise<any> {

    const response = await fetch('https://api.anthropic.com/v1/messages', {
      method: 'POST',
      headers: {
        'x-api-key': this.apiKey,
        'Content-Type': 'application/json',
        'anthropic-version': '2023-06-01',
      },
      body: JSON.stringify({
        model: model,
        max_tokens: 2000,
        messages: [
          { role: 'user', content: prompt }
        ],
        temperature: 0.1,
      }),
    })

    if (!response.ok) {
      console.error(`Anthropic API error: ${response.status} ${response.statusText}`)
      
      throw new Error(`Anthropic API error: ${response.status} ${response.statusText}`)
    }

    const data = await response.json()
    const result = data.content[0].text
    
    return result
  }

  getCost(tokens: number, model: string): number {
    const costs: Record<string, number> = {
      'claude-3-haiku-20240307': 0.00025 / 1000,
      'claude-3-5-sonnet-20241022': 0.003 / 1000,
      'claude-3-opus-20240229': 0.015 / 1000,
    }
    return tokens * (costs[model] || 0.00025 / 1000)
  }
}

// Ollama provider (fallback)
class OllamaProvider implements LLMProvider {
  name = 'ollama'
  private baseUrl: string

  constructor(baseUrl: string = 'http://localhost:11434') {
    this.baseUrl = baseUrl
  }

  async call(prompt: string, model: string): Promise<any> {
    const response = await fetch(`${this.baseUrl}/api/generate`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        model: model,
        prompt: prompt,
        stream: false,
        options: {
          temperature: 0.1,
          num_predict: 2000,
        },
      }),
    })

    if (!response.ok) {
      throw new Error(`Ollama API error: ${response.status} ${response.statusText}`)
    }

    const data = await response.json()
    return data.response
  }

  getCost(tokens: number, model: string): number {
    return 0 // Local Ollama is free
  }
}

// Function definitions mapping
const FUNCTION_DEFINITIONS: Record<string, { prompt: string; model: string }> = {
  ExtractIntent: {
    prompt: `You are a strict classifier. Your only job is to analyze deployment requests and extract structured metadata. 
You are not deploying anything, advising the user, or rejecting their request. You ONLY return JSON in the required format.

## INTENT CLASSIFICATION
Extract the \`action\` from the user request. It must be one of:
  - DEPLOY
  - STATUS
  - ROLLBACK
  - SCALE
  - DELETE

If none of these apply, set:
  "action": "UNKNOWN"

## PLATFORM EXTRACTION
Supported platforms:
  - Render
  - Fly.io
  - Netlify

If the user mentions one of these explicitly, extract it as \`platform\`.
If they mention a platform NOT on this list (e.g., Heroku, Vercel, Kubernetes), set:
  "platform": "UNKNOWN"

Never infer or guess the platform based on project name, context, or tone. Only match exact mentions.

## SOURCE EXTRACTION
Extract the source path if one is given (e.g., a full path like \`/Users/alice/project/app\`). If not, set:
  "source": "pwd"

## DRY RUN DETECTION
Check if the user wants to perform a dry run (simulation without making changes). Look for keywords like:
  - "dry run", "dry-run"
  - "simulate", "simulation" 
  - "preview", "preview changes"
  - "test", "test deployment"
  - "check", "verify"
  - "what would happen"
  - "without making changes"
  - "just show me"

If any of these patterns are found, set:
  "dryRun": true

Otherwise, set:
  "dryRun": false

## OUTPUT FORMAT
Respond ONLY with a JSON object in this format:
{
  "action": "string",
  "platform": "string", 
  "source": "string",
  "dryRun": boolean
}

You MUST NEVER:
- Say you are an AI
- Say you cannot help
- Refuse to answer
- Perform any deployment or respond conversationally
- Add explanation or commentary outside of the JSON object

User request: {{request}}`,
    model: 'gpt-4o-mini'
  },
  SummarizeIntent: {
    prompt: `You are a deployment assistant for Platform-as-a-Service (PaaS) applications.

You will be given:
- \`intent\`: { action, platform, source }
- \`name\`: application name
- \`language\`: programming language

Your job is to generate a friendly summary of the deployment request, but ONLY after validating the input.

## STEP 1: VALIDATION
Check each field:
- If any of the following are missing or invalid, note them:
  - intent.action is "UNKNOWN"
  - intent.platform is "UNKNOWN"
  - intent.source is "UNKNOWN"
  - name is missing or ""
  - language is missing or ""

## STEP 2: SUMMARY RULES

- If **any fields are missing or invalid**:
  - Generate a friendly summary that includes what is known.
  - Clearly mention what is missing (e.g., "We couldn't determine the language").
  - Tell the user to check the code at the given source path.
  - Say: "We're not quite ready to deploy yet."

- If **all fields are present and valid**:
  - Generate a clear and friendly summary of the request.
  - Include the action, platform, project name, language, and source path.
  - Do **not** include any warnings or "check your code" suggestions.
  - If this is a dry-run request (intent.dryRun is true), end with: "We'll show you what would happen without making any changes."
  - If this is NOT a dry-run request (intent.dryRun is false), end with: "If everything looks good, we'll take care of deploying it for you."

## STYLE
- Keep it friendly and simple.
- Do not guess or infer any missing values.
- Only include fallback/warning messages when something is actually missing.

Please summarize this deployment request in friendly, simple language so a non-technical user can confirm the details. 
If anything important is missing, clearly explain what's missing and what to check. Otherwise, give a clean summary with no extra warnings.

Intent: {{intent}}
Language: {{language}}
Name: {{name}}

Respond with JSON: {"summary": "your summary here"}`,
    model: 'gpt-4o-mini'
  },
  SummarizeSteps: {
    prompt: `I will provide you with a list of steps. Your task is to summarize these steps in a short, conversational paragraph using the third person — 
as if someone is describing the plan using "We will..." phrasing.

## Important rules:
- Do not add any steps or details.
- Do not remove or skip any steps.
- Every step in the original list must be clearly reflected in the summary.
- Keep the tone friendly and conversational.

Finally, end the summary letting the reader know we will do our best to retry any failed steps.
Here is the list of steps:
{{steps}}

Respond with JSON: {"summary": "your summary here"}`,
    model: 'gpt-4o-mini'
  },
  SummarizeDeployError: {
    prompt: `You are an assistant that explains technical error messages in a friendly, conversational, and apologetic tone.

{% if violations|length > 0 %}
Your previous answer violated platform-specific rules:
{% for v in violations %}
 - {{ v }}
{% endfor %}
Please regenerate without these issues.
{% endif %}

## Terminology

- **Prod** = the name of the CLI the user is using
- **User** = developer using Prod
- **Render** and **Fly.io** = deployment platforms (not container registries)
- **ECR** = internal Docker image registry, not directly used by the user

## Context and Rules for Docker Image Pushes

- Docker builds and pushes are handled internally using AWS ECR. Users **do not** interact with ECR directly.
- Deployment platforms (Render, Fly.io) are **not** the container registry. They are the deployment targets.
- When image push errors occur (e.g. 503 or name resolution failures from the push-token endpoint), they usually indicate **transient internal API issues** or **network issues**.
- DO NOT suggest \`docker login\`, \`docker push\`, or any other Docker CLI command.
- DO NOT suggest commands that do not exist in the Prod CLI, such as \`prod login\`.
- To check authentication configuration:
  - For Render: inspect the \`$RENDER_API_KEY\` environment variable or verify the \`api_key\` value in \`~/.render/cli.yaml\`
  - For Fly.io: inspect the \`$FLY_API_TOKEN\` environment variable or verify the config in \`~/.fly/config.yml\`
- If the error is related to name resolution or 503s, suggest:
  - Retrying after a few moments
  - Checking internet or DNS settings if the problem persists

## Context and Rules for Docker Daemon Errors

- If a Docker build fails with an error like "cannot connect to local Docker daemon", this means the local Docker service is **not running or not reachable**.
- This is **not a network or DNS issue**. Never suggest checking DNS, retrying, or verifying internet connectivity.
- You are given an \`os\` field. Based on its value, tailor the suggestion appropriately. You **must not** reference other operating systems.

### Per-OS Guidance

- If \`os == "darwin"\`:
  - Remind the user that Docker Desktop must be running in the background.
  - Suggest launching Docker Desktop from the Applications folder if it's not already running.

- If \`os == "windows"\`:
  - Remind the user that Docker Desktop must be running in the background.
  - Suggest launching it from the Start Menu if needed.

- If \`os == "linux"\`:
  - Remind the user that the Docker daemon must be running.
  - Suggest starting it with: \`sudo systemctl start docker\`

## Dashboard Access

- Do NOT suggest using the \`render\` or \`flyctl\` CLIs.
- If a suggestion involves checking the dashboard for the relevant deployment platform, always include a shell command to open the page:
  - Render:
    - macOS: \`open https://dashboard.render.com\`
    - Linux: \`xdg-open https://dashboard.render.com\`
    - Windows (optional): \`start https://dashboard.render.com\`
  - Fly.io:
    - macOS: \`open https://fly.io/dashboard\`
    - Linux: \`xdg-open https://fly.io/dashboard\`
    - Windows (optional): \`start https://fly.io/dashboard\`
- Do NOT assume the user will manually open a browser or know where to go.

### Tone & Style

- You can phrase the message naturally, but the remediation must match the user's OS.
- Write as if you're helping a peer get unblocked — kind, concise, and technically accurate.
- Do **not** mention instructions for other platforms.
- Do **not** generalize by saying "on macOS or Linux" or similar.
- Do **not** suggest retrying or checking internet/DNS settings unless the context rules allow it.
- Do **not** include platform-agnostic phrasing like "try restarting Docker" — always be OS-specific.

Use this context to:
1. Explain what went wrong in plain, empathetic language. Do not blame the user or imply they made a mistake.
2. Identify the most likely causes **based only on the provided information** (do not guess or hallucinate).
3. Suggest one or more specific, **relevant** remediation steps:
   - Clearly describe what the user should do and why.
   - Include an OS-appropriate CLI command if applicable.
   - Only suggest Prod CLI commands that are known to exist, or standard shell commands (e.g., echo $VAR). Do not invent commands.
   - Never use third-party or platform-specific CLIs (e.g., render, flyctl, docker, etc.).
   - Always include direct, OS-specific commands for accessing web dashboards.
   - If the necessary Prod command is unknown, say "check your Prod configuration" instead of making up a command.
   - Refer to the CLI tool as **Prod**.
   - Do **not** suggest running application code directly (e.g., \`node app.js\`) or debugging client-side API request logic — Prod handles all API communication.
   - Do **not** suggest modifying or reinstalling the user's project dependencies unless explicitly instructed in the error message.
   - Do **not** suggest inspecting or debugging code within the project being deployed.
   - Do **not** suggest inspecting proxy settings or firewalls unless the error explicitly references them.
   - Do not mention unrelated platforms or tools.
   - Do **not** suggest reinstalling or updating project dependencies unless explicitly required by the error message.
   - Do **not** suggest building or running the user's application unless explicitly required by the error message.
   - Do **not** assume the user edited configuration files unless the error explicitly references them.
   - Do **not** invent or suggest Prod commands that are not explicitly described.
   - If checking credentials/configuration:
     - Render: \`echo $RENDER_API_KEY\` and check \`~/.render/cli.yaml\`
     - Fly.io: \`echo $FLY_API_TOKEN\` and check \`~/.fly/config.yml\`
   - Remediations must be strictly relevant to the actual root cause.
   - Only recommend Docker-specific steps if the error mentions Docker.

Auth Notes:
- For Render deployments, authentication is handled via:
  - The \`$RENDER_API_KEY\` environment variable
  - The \`~/.render/cli.yaml\` file
- For Fly.io deployments, authentication is handled via:
  - The \`$FLY_API_TOKEN\` environment variable
  - The \`~/.fly/config.yml\` file

Special Cases:
- If the error includes a 409 status and a message like "already in use", it's likely due to a naming conflict for a resource.
- In this case, suggest that the user check if the resource already exists, and consider deleting it or choosing a new name.
- For databases, mention checking in the appropriate platform dashboard or using the Prod CLI (if applicable) to clean up unused resources.

You are given:
- An error message (typically from a Go program)
- The user's operating system
- The deployment platform (e.g., Render, Fly.io, Heroku)
- The action being attempted (e.g., "list workspaces")
- The code path or component where the error occurred
- Specifications about the project (e.g., language, name, build command, dependencies, etc.)

Your task:
1. Explain what went wrong in plain, empathetic language.
2. Identify the most likely cause based only on the provided info.
3. Suggest specific, relevant remediation steps that align with the provided rules and OS.

## Error Context
- **Error Message**: {{errorMsg}}
- **Operating System**: {{os}}
- **Deployment Action**: {{intent}}
- **Project Spec**: {{spec}}

Respond with JSON: {"summary": "error summary", "remediations": [{"description": "remediation description", "cliCommand": "command to run"}]}`,
    model: 'claude-3-5-sonnet-20241022'
  }
}

// Rate limiting storage (in-memory for simplicity)
const rateLimitStore = new Map<string, { count: number; resetTime: number }>()

// Rate limiting function
function checkRateLimit(userId: string, limit: number = 60, windowMs: number = 60000): boolean {
  const now = Date.now()
  const key = userId || 'anonymous'
  const userLimit = rateLimitStore.get(key)

  if (!userLimit || now > userLimit.resetTime) {
    rateLimitStore.set(key, { count: 1, resetTime: now + windowMs })
    return true
  }

  if (userLimit.count >= limit) {
    return false
  }

  userLimit.count++
  return true
}

// Main handler
serve(async (req) => {
  const startTime = Date.now()

  // Log function entry
  console.log(`[${new Date().toISOString()}] LLM-Proxy function called: ${req.method} ${req.url}`)

  // CORS headers
  const corsHeaders = {
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Headers': 'authorization, x-client-info, apikey, content-type',
    'Access-Control-Allow-Methods': 'POST, OPTIONS',
  }

  // Log client information
  const clientType = req.headers.get('X-Client-Type') || 'Unknown'
  const requestSource = req.headers.get('X-Request-Source') || 'Unknown'
  const userAgent = req.headers.get('User-Agent') || 'Unknown'
  

  // Handle preflight requests
  if (req.method === 'OPTIONS') {
    return new Response(null, { status: 200, headers: corsHeaders })
  }

  // Handle log file download
  if (req.method === 'GET' && req.url.includes('/logs')) {
    try {
      const logContent = await Deno.readTextFile('/tmp/llm-proxy.log')
      return new Response(logContent, {
        headers: { 
          ...corsHeaders, 
          'Content-Type': 'text/plain',
          'Content-Disposition': 'attachment; filename="llm-proxy.log"'
        }
      })
    } catch (error) {
      return new Response('Log file not found or empty', {
        status: 404,
        headers: { ...corsHeaders, 'Content-Type': 'text/plain' }
      })
    }
  }

  // Only allow POST requests for LLM calls
  if (req.method !== 'POST') {
    return new Response(
      JSON.stringify({ success: false, error: { code: 'METHOD_NOT_ALLOWED', message: 'Only POST requests are allowed' } }),
      { status: 405, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }

  try {
    // Parse request body
    const rawBody = await req.json()
    console.log(`[${new Date().toISOString()}] Processing request for function: ${rawBody.function || 'BAML request'}`)

    // Check if this is a BAML request (OpenAI format) or a direct function call
    let requestBody: LLMProxyRequest
    
    if (rawBody.messages && Array.isArray(rawBody.messages)) {
      // This is a BAML request (OpenAI format)
      
      // Extract the user message content
      const userMessage = rawBody.messages.find((msg: any) => msg.role === 'user')
      const userContent = userMessage ? userMessage.content : ''
      
      // Convert to our expected format
      requestBody = {
        function: 'ExtractIntent',
        args: { request: userContent },
        model_preference: rawBody.model || 'gpt-4o-mini',
        fallback_enabled: true
      }
    } else {
      // This is a direct function call
      requestBody = rawBody as LLMProxyRequest
    }
    

    // Validate request
    if (!requestBody.function || !requestBody.args) {
      return new Response(
        JSON.stringify({ 
          success: false, 
          error: { code: 'INVALID_REQUEST', message: 'Missing required fields: function and args' } 
        }),
        { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // Initialize Supabase client
    const supabaseUrl = Deno.env.get('SUPABASE_URL')!
    const supabaseServiceKey = Deno.env.get('SUPABASE_SERVICE_ROLE_KEY')!
    const supabase = createClient(supabaseUrl, supabaseServiceKey)

    // Get user ID from JWT token
    const authHeader = req.headers.get('Authorization')
    
    let userId = 'anonymous'
    
    if (authHeader) {
      try {
        const token = authHeader.replace('Bearer ', '')
        const { data: { user } } = await supabase.auth.getUser(token)
        userId = user?.id || 'anonymous'
      } catch (error) {
        console.error(`[${new Date().toISOString()}] DEBUG: Token validation failed:`, error)
        console.warn('Failed to get user from token:', error)
      }
    } else {
    }

    // Rate limiting
    if (!checkRateLimit(userId, 60, 60000)) { // 60 requests per minute
      return new Response(
        JSON.stringify({ 
          success: false, 
          error: { 
            code: 'RATE_LIMITED', 
            message: 'Rate limit exceeded. Please try again later.',
            retry_after: 60
          },
          fallback_available: true
        }),
        { status: 429, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // Get function definition
    const functionDef = FUNCTION_DEFINITIONS[requestBody.function]
    if (!functionDef) {
      return new Response(
        JSON.stringify({ 
          success: false, 
          error: { code: 'FUNCTION_NOT_FOUND', message: `Function ${requestBody.function} not found` } 
        }),
        { status: 404, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // Build prompt from template
    let prompt = functionDef.prompt
    for (const [key, value] of Object.entries(requestBody.args)) {
      prompt = prompt.replace(new RegExp(`{{${key}}}`, 'g'), String(value))
    }

    // Determine model to use
    const preferredModel = requestBody.model_preference || functionDef.model
    const fallbackEnabled = requestBody.fallback_enabled !== false

    // Initialize providers
    const providers: LLMProvider[] = []
    
    // Add commercial providers if API keys are available
    const openaiKey = Deno.env.get('OPENAI_API_KEY')
    const openaiLogMessage = `[${new Date().toISOString()}] OpenAI API key available: ${openaiKey ? 'YES' : 'NO'}\n`
    
    
    if (openaiKey) {
      
      providers.push(new OpenAIProvider(openaiKey))
    }
    
    const anthropicKey = Deno.env.get('ANTHROPIC_API_KEY')
    if (anthropicKey) {
      providers.push(new AnthropicProvider(anthropicKey))
    }

    // Add Ollama as fallback
    if (fallbackEnabled) {
      providers.push(new OllamaProvider())
    }

    if (providers.length === 0) {
      return new Response(
        JSON.stringify({ 
          success: false, 
          error: { code: 'NO_PROVIDERS', message: 'No LLM providers available' } 
        }),
        { status: 503, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // Try providers in order
    let lastError: Error | null = null
    let result: any = null
    let modelUsed = ''
    let tokensUsed = 0
    let cost = 0

    for (const provider of providers) {
      try {
        console.log(`[${new Date().toISOString()}] Trying provider: ${provider.name}`)
        
        // Determine model for this provider
        let model = preferredModel
        if (provider.name === 'openai' && !model.startsWith('gpt-')) {
          model = 'gpt-3.5-turbo'  // Try with a more widely available model
        } else if (provider.name === 'anthropic' && !model.startsWith('claude-')) {
          model = 'claude-3-haiku-20240307'
        } else if (provider.name === 'ollama') {
          model = 'llama3.1'
        }

        result = await provider.call(prompt, model)
        modelUsed = `${provider.name}:${model}`
        console.log(`[${new Date().toISOString()}] Success with provider: ${provider.name}, model: ${model}`)
        
        // Estimate tokens (rough approximation)
        tokensUsed = Math.ceil(prompt.length / 4) + Math.ceil(result.length / 4)
        cost = provider.getCost(tokensUsed, model)
        
        break
        
      } catch (error) {
        console.error(`Provider ${provider.name} failed:`, error)
        console.error(`Error details:`, error.message)
        lastError = error as Error
        
        // If this is the last provider and fallback is enabled, continue to next
        if (provider === providers[providers.length - 1]) {
          break
        }
      }
    }

    if (!result) {
      return new Response(
        JSON.stringify({ 
          success: false, 
          error: { 
            code: 'ALL_PROVIDERS_FAILED', 
            message: `All LLM providers failed. Last error: ${lastError?.message}` 
          },
          fallback_available: false
        }),
        { status: 503, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // Parse JSON response
    let parsedResult: any
    try {
      parsedResult = JSON.parse(result)
    } catch (error) {
      // If JSON parsing fails, wrap the result
      parsedResult = { result: result }
    }

    const responseTime = Date.now() - startTime

    // Log usage to database (optional)
    try {
      await supabase
        .from('llm_usage_logs')
        .insert({
          user_id: userId,
          function_name: requestBody.function,
          model_used: modelUsed,
          tokens_used: tokensUsed,
          cost: cost,
          response_time_ms: responseTime,
          success: true
        })
    } catch (error) {
      console.warn('Failed to log usage:', error)
    }

    // Check if this was a BAML request (OpenAI format) and return OpenAI-compatible response
    if (rawBody.messages && Array.isArray(rawBody.messages)) {
      console.log(`[${new Date().toISOString()}] Returning OpenAI-compatible response for BAML`)
      // Return OpenAI-compatible response for BAML
      const openaiResponse = {
        id: `chatcmpl-${Date.now()}`,
        object: "chat.completion",
        created: Math.floor(Date.now() / 1000),
        model: modelUsed,
        choices: [
          {
            index: 0,
            message: {
              role: "assistant",
              content: JSON.stringify(parsedResult)
            },
            finish_reason: "stop"
          }
        ],
        usage: {
          prompt_tokens: 0,
          completion_tokens: tokensUsed,
          total_tokens: tokensUsed
        }
      }
      
      
      return new Response(
        JSON.stringify(openaiResponse),
        { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    } else {
      // Return our custom format for direct function calls
      const response: LLMProxyResponse = {
        success: true,
        result: parsedResult,
        metadata: {
          model_used: modelUsed,
          tokens_used: tokensUsed,
          cost: cost,
          response_time_ms: responseTime
        }
      }

      return new Response(
        JSON.stringify(response),
        { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

  } catch (error) {
    console.error('LLM Proxy error:', error)
    
    const response: LLMProxyResponse = {
      success: false,
      error: {
        code: 'INTERNAL_ERROR',
        message: 'An internal error occurred'
      }
    }

    return new Response(
      JSON.stringify(response),
      { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }
})
