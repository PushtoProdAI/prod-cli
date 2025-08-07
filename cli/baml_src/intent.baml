

class Intent {
    action string
    platform string
    source string
    dryRun bool
  }

class Summary {
    summary string
  }

class Remediation {
    description string
    cliCommand string
  }

class Error {
    summary string
    remediations Remediation[] 
  }

class ProjectSpec {
	name                string
	language            string
	serviceRequirements ServiceRequirement[]
	buildCommand        string
	startCommand        string
}

class ServiceRequirement{
	type     string
	provider string
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
    - If this is a dry-run request (intent.dryRun is true), end with: "We'll show you what would happen without making any changes."
    - If this is NOT a dry-run request (intent.dryRun is false), end with: "If everything looks good, we'll take care of deploying it for you."

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

function SummarizeDeployError(errorMsg: string, intent: Intent, spec: ProjectSpec, os: string) -> Error {
  client "OllamaClient"
  prompt #"
    {{ _.role("system") }}

You are an assistant that explains technical error messages in a friendly, conversational, and apologetic tone.


## Terminology

- **Prod** = the name of the CLI the user is using
- **User** = developer using Prod
- **Render** = the deployment platform (not the container registry)
- **ECR** = internal Docker image registry, not directly used by the user

## Context and Rules for Docker Image Pushes

- Docker builds and pushes are handled internally using AWS ECR. Users **do not** interact with ECR directly.
- Render is **not** the container registry. It is the deployment target.
- When image push errors occur (e.g. 503 or name resolution failures from the push-token endpoint), they usually indicate **transient internal API issues** or **network issues**.
- DO NOT suggest `docker login`, `docker push`, or any other Docker CLI command.
- DO NOT suggest commands that do not exist in the Prod CLI, such as `prod login`.
- To check Render authentication configuration, suggest:
  - Inspecting the `$RENDER_API_KEY` environment variable
  - Verifying the `api_key` value in `~/.render/cli.yaml`
- If the error is related to name resolution or 503s, suggest:
  - Retrying after a few moments
  - Checking internet or DNS settings if the problem persists

## Context and Rules for Docker Daemon Errors

- If a Docker build fails with an error like "cannot connect to local Docker daemon", this means the local Docker service is **not running or not reachable**.
- This is **not a network or DNS issue**. Never suggest checking DNS, retrying, or verifying internet connectivity.
- You are given an `os` field. Based on its value, tailor the suggestion appropriately. You **must not** reference other operating systems.

### Per-OS Guidance

- If `os == "darwin"`:
  - Remind the user that Docker Desktop must be running in the background.
  - Suggest launching Docker Desktop from the Applications folder if it's not already running.

- If `os == "windows"`:
  - Remind the user that Docker Desktop must be running in the background.
  - Suggest launching it from the Start Menu if needed.

- If `os == "linux"`:
  - Remind the user that the Docker daemon must be running.
  - Suggest starting it with: `sudo systemctl start docker`

## Render Dashboard Access

- Do NOT suggest using the `render` CLI.
- If a suggestion involves checking the Render dashboard, always include a shell command to open the page:
  - macOS: `open https://dashboard.render.com`
  - Linux: `xdg-open https://dashboard.render.com`
  - Windows (optional): `start https://dashboard.render.com`

Do NOT assume the user will manually open a browser or know where to go.

### Tone & Style

- You can phrase the message naturally, but the remediation must match the user's OS.
- Write as if you're helping a peer get unblocked — kind, concise, and technically accurate.
- Do **not** mention instructions for other platforms.
- Do **not** generalize by saying "on macOS or Linux" or similar.
- Do **not** suggest retrying or checking internet/DNS settings.
- Do **not** include platform-agnostic phrasing like "try restarting Docker" — always be OS-specific.


Use this context to:
1. Explain what went wrong in plain, empathetic language. Do not blame the user or imply they made a mistake.
2. Identify the most likely causes **based only on the provided information** (do not guess or hallucinate).
3. Suggest one or more specific, **relevant** remediation steps:
   - Clearly describe what the user should do and why.
   - Include an OS-appropriate CLI command if applicable.
   - Only suggest Prod CLI commands that are known to exist, or standard shell commands (e.g., echo $VAR). Do not invent commands 
   - Never use third-party or platform-specific CLIs (e.g., render, flyctl, docker, etc.).
   - Always include direct, OS-specific commands for accessing web dashboards
   - If the necessary Prod command is unknown, say "check your Prod configuration" instead of making up a command.
   - Refer to the CLI tool as **Prod** (not "your CLI" or "the CLI")
   - Do **not** suggest running application code directly (e.g., `node app.js`) or debugging client-side API request logic — Prod handles all API communication, and client code is only built, not executed.
   - Do **not** suggest modifying or reinstalling the user’s project dependencies (e.g., `npm install`, `pip install`, etc.) unless explicitly instructed in the error message.
   - For **network or API connection errors**, do **not** suggest inspecting or debugging code within the project being deployed — this code does **not** initiate or handle any network communication.
   - Do **not** suggest inspecting proxy settings or firewalls **unless** the error message explicitly references a proxy, timeout, DNS failure, or connection refusal.
   - Do not mention unrelated platforms or tools (e.g., VMware, Tanzu).
   - Do **not** suggest reinstalling or updating project dependencies (e.g., `npm install`, `pip install`, etc.) based on the project language alone — only recommend this if the error message explicitly mentions a missing dependency or build failure.
   - Do **not** suggest building or running the user's application (e.g., `npm run build`, `node app.js`) unless the error specifically indicates a problem with build output or startup.
   - Do **not** assume the user edited configuration files unless the error message directly references a misconfiguration or parsing error. 
   - Do **not** invent or suggest `Prod` commands that are not explicitly described in the context. If checking credentials or configuration, prefer standard environment variable commands (like `echo $RENDER_API_KEY`) or give a generic instruction.
   - If the necessary `Prod` command is unknown, say "check your Prod configuration" instead of making up a command.
   - If suggesting how to check, give shell commands like `echo $RENDER_API_KEY` and mention the config file path (`~/.render/cli.yaml`)
   - Remediations must be strictly relevant to the actual root cause.
   - Do not include generic suggestions like checking Docker or CLI configuration unless they are directly related to the failure.
   - Only recommend Docker-specific steps if the error mentions Docker (e.g., cannot connect to Docker daemon or docker build failed).

   Auth Notes:
    - For Render deployments, authentication is handled via:
    - The `$RENDER_API_KEY` environment variable, or
    - The `~/.render/cli.yaml` file, which may contain an API key or auth token.
    - If either is missing or invalid, API requests may fail with status 400 or 401.

   Special Cases:
    - If the error includes a 409 status and a message like "already in use", it's likely due to a naming conflict for a resource (e.g., database, service).
    - In this case, suggest that the user check if the resource already exists, and consider deleting it or choosing a new name.
    - For databases, mention checking in the Render dashboard or using the Prod CLI (if applicable) to clean up unused resources.


    {{ _.role("user") }}
    You are given:
    - An error message (typically from a Go program)
    - The user’s operating system
    - The deployment platform (e.g., Render, Fly.io, Heroku)
    - The action being attempted (e.g., "list workspaces")
    - The code path or component where the error occurred
    - Specifications about the project (e.g., language, name, build command, dependencies, etc.)

    Your task:
    1. Explain what went wrong in plain, empathetic language.
    2. Identify the most likely cause based only on the provided info.
    3. Suggest specific, relevant remediation steps that align with the provided rules and OS.

    ## Error Context
    - **Error Message**: {{ errorMsg }}
    - **Operating System**: {{ os }}
    - **Deployment Action**: {{ intent }}
    - **Project Spec**: {{ spec }}

    {{ctx.output_format }}

  "#
}


// Place all test blocks after type and function definitions

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

test testDryRunIntent {
  functions [ExtractIntent]
  args {
    request #"
     Can you simulate deploying /Users/raulbarroso/code/meroxa/prod/test-projects/fastapi-app to render without making any changes?
    "#
  }
}

test errorSummary {
  functions [SummarizeDeployError]
  args {
    intent {
      action "DEPLOY"
      platform "Render"
      source "/path/to/app"
      dryRun false
    }
    errorMsg "failed to execute render steps: failed to execute step step-1: failed to create postgres: API request failed with status 409: {\"message\":\"name: (hockey-pool-app_db) already in use\"}\n"
    os "darwin"
    spec {
      name "foo"
      language "nodejs"
      serviceRequirements []
      buildCommand "npm run build"
      startCommand "npm start"
    }
  }
}
