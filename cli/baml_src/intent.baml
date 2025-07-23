

class Intent {
    action string
    platform string
    source string
  }

class Summary {
    summary string
  }



function ExtractIntent(request: string) -> Intent {
  client "OllamaClient"
  prompt #"
 {{ _.role("system") }}
You are a strict classifier. Your only job is to analyze deployment requests and extract structured metadata. 
  You are not deploying anything, advising the user, or rejecting their request. You ONLY return JSON in the required format.

  ## INTENT CLASSIFICATION
  Extract the `action` from the user request. It must be one of:
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

  If the user mentions one of these explicitly, extract it as `platform`.
  If they mention a platform NOT on this list (e.g., Heroku, Vercel, Kubernetes), set:
    "platform": "UNKNOWN"

  Never infer or guess the platform based on project name, context, or tone. Only match exact mentions.

  ## SOURCE EXTRACTION
  Extract the source path if one is given (e.g., a full path like `/Users/alice/project/app`). If not, set:
    "source": "pwd"

  ## OUTPUT FORMAT
  Respond ONLY with a JSON object in this format:
  {{ctx.output_format}}

  You MUST NEVER:
  - Say you are an AI
  - Say you cannot help
  - Refuse to answer
  - Perform any deployment or respond conversationally
  - Add explanation or commentary outside of the JSON object

    {{ _.role("user") }}
    {{ request }}

    {{ ctx.output_format }}
  "#
}

function SummarizeIntent(intent: Intent, name: string, language: string) -> Summary {
  client "OllamaClient"
  prompt #"
    {{ _.role("system") }}
  You are a deployment assistant for Platform-as-a-Service (PaaS) applications.

  You will be given:
  - `intent`: { action, platform, source }
  - `name`: application name
  - `language`: programming language

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
    - Say: “We’re not quite ready to deploy yet.”

  - If **all fields are present and valid**:
    - Generate a clear and friendly summary of the request.
    - Include the action, platform, project name, language, and source path.
    - Do **not** include any warnings or "check your code" suggestions.
    - End with: “If everything looks good, we’ll take care of deploying it for you.”

  ## STYLE
  - Keep it friendly and simple.
  - Do not guess or infer any missing values.
  - Only include fallback/warning messages when something is actually missing.
    {{ _.role("user") }}

  Please summarize this deployment request in friendly, simple language so a non-technical user can confirm the details. 
  If anything important is missing, clearly explain what's missing and what to check. Otherwise, give a clean summary with no extra warnings.

    Intent: {{ intent }}
    Language: {{ language }}
    Name: {{ name }}

 
    {{ctx.output_format }}
  "#
}

function SummarizeSteps(steps: string[]) -> Summary {
  client "OllamaClient"
  prompt #"
    {{ _.role("user") }}

  I will provide you with a list of steps. Your task is to summarize these steps in a short, conversational paragraph using the third person — 
  as if someone is describing the plan using "We will..." phrasing.

  ## Important rules:
  - Do not add any steps or details.
  - Do not remove or skip any steps.
  - Every step in the original list must be clearly reflected in the summary.
  - Keep the tone friendly and conversational.

  Finally, end the summary letting the reader know we will do our best to retry any failed steps.
  Here is the list of steps:
  {{ steps }}

 
    {{ctx.output_format }}
  "#
}

function SummarizeDeployError(errorMsg: string) -> Summary {
  client "OllamaClient"
  prompt #"
    {{ _.role("user") }}

  You are an assistant that explains technical error messages in a friendly, conversational, and apologetic tone. When given an error message (typically from a Go program), 
  summarize what went wrong in simple terms. Show empathy, like you're trying to help someone understand what happened without blaming them. Do not invent fixes or add details 
  — just restate the error more clearly and kindly.

  {{ errorMsg }}

 
    {{ctx.output_format }}
  "#
}


test testIntent {
  functions [ExtractIntent]
  args {
    request #"
     Deploy my code here: /Users/nstehr/code/prod/test-projects/node-ap1p to my heroku account
    "#
  }
}

test intentSummary 
{
  functions [SummarizeIntent]
  args {
    intent {
      action "DEPLOY"
      platform "Render"
      source "/path/to/app"
    }
    name "foo"
    language ""
  }
}

