

class Intent {
    action string
    platform string
    source string
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

class EnvVarCandidate {
	varName string
	file    string
	line    int
	context string
}

class EnvVarCategory {
  isDBRelated bool
  role string
  dbType string
  isSensitive bool
  sensitivityReason string
}

class RouteCandidate {
	method  string
	path    string
	file    string
	line    int
	context string
  }
 
 class Route {
    method string
    path string
    confidence float
  }

class CategorizedRoutes {
    routes Route[]
    recommended Route
}

class BuildOutputCandidate {
	default       string
	source        string
	framework     string
	context       string
}

class BuildOutput {
    path string
}
 

class LauncherFile {
    name string
    content string
  }

class LaunchContext {
	launchers LauncherFile[]
	readme    string
}

class LaunchCommand {
    command string
  }


function ExtractIntent(request: string) -> Intent {
  client "ProxyClient"
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
    - Netlify
    - Vercel
    - Heroku
    - AWS

  NOTE: AWS is Amazon Web Services. So consider strings like "deploy to Amazon" having a target of AWS. 
  
  If the user mentions one of these explicitly, extract it as `platform`.
  If they mention a platform NOT on this list (e.g. Kubernetes), set:
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

function SummarizeIntent(intent: Intent, name: string, language: string, detectedPlatforms: string[]) -> Summary {
  client "ProxyClient"
  prompt #"
    {{ _.role("system") }}
  You are a deployment assistant for Platform-as-a-Service (PaaS) applications.

  You will be given:
  - `intent`: { action, platform, source }
  - `name`: application name
  - `language`: programming language
  - `detectedPlatforms`: list of platforms where the project is currently deployed (for rollback)

  Your job is to generate a friendly summary of the request, but ONLY after validating the input.

  ## STEP 1: VALIDATION
  Check each field:
  - If any of the following are missing or invalid, note them:
    - intent.action is "UNKNOWN"
    - intent.platform is "UNKNOWN" AND no detectedPlatforms provided
    - intent.source is "UNKNOWN"
    - name is missing or ""
    - language is missing or ""

  ## STEP 2: SUMMARY RULES FOR ROLLBACK

  If intent.action is "ROLLBACK":
    - Use "rollback" terminology, NOT "deploy" or "deployment"
    - Platform handling:
      1. If detectedPlatforms has exactly 1 platform: "We found your application deployed on [Platform]. We'll rollback your deployment there."
      2. If detectedPlatforms has multiple platforms: "We found your application deployed on multiple platforms: [Platform1, Platform2]. We'll ask you to choose which one to rollback before proceeding."
      3. If detectedPlatforms is empty or intent.platform is UNKNOWN: "We couldn't find your application deployed on any platform. Please make sure you're in the correct directory, or specify the platform explicitly in your request (e.g., 'rollback to Render')."
    - Include the project name, language, and source path
    - End with: "If everything looks good, we'll take care of rolling it back for you."

  ## STEP 3: SUMMARY RULES FOR DEPLOY

  If intent.action is "DEPLOY":
    - If **any fields are missing or invalid**:
      - Generate a friendly summary that includes what is known.
      - Clearly mention what is missing (e.g., "We couldn't determine the language").
      - Tell the user to check the code at the given source path.
      - Say: "We're not quite ready to deploy yet."

    - If **all fields are present and valid**:
      - Generate a clear and friendly summary of the request.
      - Include the action, platform, project name, language, and source path.
      - Do **not** include any warnings or "check your code" suggestions.
      - End with: "If everything looks good, we'll take care of deploying it for you."

  ## STYLE
  - Keep it friendly and simple.
  - Do not guess or infer any missing values.
  - Only include fallback/warning messages when something is actually missing.
    {{ _.role("user") }}

  Please summarize this request in friendly, simple language so a non-technical user can confirm the details. 
  If anything important is missing, clearly explain what's missing and what to check. Otherwise, give a clean summary with no extra warnings.

    Intent: {{ intent }}
    Language: {{ language }}
    Name: {{ name }}
    Detected Platforms: {{ detectedPlatforms }}

 
    {{ctx.output_format }}
  "#
}

function SummarizeSteps(steps: string[]) -> Summary {
  client "ProxyClient"
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

function SummarizeDeployError(errorMsg: string, intent: Intent, spec: ProjectSpec, os: string, violations: string[]) -> Error {
  client "ProxyClient"
  prompt #"
  {{ _.role("system") }}

You are an assistant that explains technical error messages in a friendly, conversational, and apologetic tone.

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
- DO NOT suggest `docker login`, `docker push`, or any other Docker CLI command.
- DO NOT suggest commands that do not exist in the Prod CLI, such as `prod login`.
- To check authentication configuration:
  - For Render: inspect the `$RENDER_API_KEY` environment variable or verify the `api_key` value in `~/.render/cli.yaml`
  - For Fly.io: inspect the `$FLY_API_TOKEN` environment variable or verify the config in `~/.fly/config.yml`
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

## Dashboard Access

- Do NOT suggest using the `render` or `flyctl` CLIs.
- If a suggestion involves checking the dashboard for the relevant deployment platform, always include a shell command to open the page:
  - Render:
    - macOS: `open https://dashboard.render.com`
    - Linux: `xdg-open https://dashboard.render.com`
    - Windows (optional): `start https://dashboard.render.com`
  - Fly.io:
    - macOS: `open https://fly.io/dashboard`
    - Linux: `xdg-open https://fly.io/dashboard`
    - Windows (optional): `start https://fly.io/dashboard`
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
   - Do **not** suggest running application code directly (e.g., `node app.js`) or debugging client-side API request logic — Prod handles all API communication.
   - Do **not** suggest modifying or reinstalling the user’s project dependencies unless explicitly instructed in the error message.
   - For network/API connection errors, do **not** suggest inspecting or debugging code within the project being deployed.
   - Do **not** suggest inspecting proxy settings or firewalls unless the error explicitly references them.
   - Do not mention unrelated platforms or tools.
   - Do **not** suggest reinstalling or updating project dependencies unless explicitly required by the error message.
   - Do **not** suggest building or running the user's application unless explicitly required by the error message.
   - Do **not** assume the user edited configuration files unless the error explicitly references them.
   - Do **not** invent or suggest Prod commands that are not explicitly described.
   - If checking credentials/configuration:
     - Render: `echo $RENDER_API_KEY` and check `~/.render/cli.yaml`
     - Fly.io: `echo $FLY_API_TOKEN` and check `~/.fly/config.yml`
   - Remediations must be strictly relevant to the actual root cause.
   - Only recommend Docker-specific steps if the error mentions Docker.

Auth Notes:
- For Render deployments, authentication is handled via:
  - The `$RENDER_API_KEY` environment variable
  - The `~/.render/cli.yaml` file
- For Fly.io deployments, authentication is handled via:
  - The `$FLY_API_TOKEN` environment variable
  - The `~/.fly/config.yml` file

Special Cases:
- If the error includes a 409 status and a message like "already in use", it's likely due to a naming conflict for a resource.
- In this case, suggest that the user check if the resource already exists, and consider deleting it or choosing a new name.
- For databases, mention checking in the appropriate platform dashboard or using the Prod CLI (if applicable) to clean up unused resources.

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

function DetermineEnvVarRoles(envVar: EnvVarCandidate, dbList: string[]) -> EnvVarCategory {
  client "ProxyClient"
  prompt #"
{{_.role("system")}}
You are an expert at classifying environment variables for deployment configuration.

Your tasks:
1. Determine if the variable is database-related or Redis/cache-related
2. Determine if the variable contains sensitive data

Classification should be based primarily on:
- The variable name
- Its direct usage
- Context around it in the provided snippet

BACKING SERVICE CLASSIFICATION RULES (DATABASE AND REDIS):

DATABASE CLASSIFICATION RULES:
1. General environment variables that represent the app's runtime stage or environment 
   (e.g., production, development, staging, test) are NOT considered DB-related unless the 
   variable name itself clearly contains database-related terms.
   Examples: NODE_ENV, RAILS_ENV, APP_ENV, ENVIRONMENT
   - These are `not_db_related` unless their name includes keywords like `db`, `database`, 
     `sql`, `mysql`, `postgres`, `mongodb`, `pg`, etc.

2. IMPORTANT: Generic server/app configuration variables are NOT database-related:
   - HOST, PORT, SERVER_HOST, SERVER_PORT → Application server configuration, NOT database
   - Only database-prefixed versions are DB-related: DB_HOST, PGHOST, MYSQL_HOST, DATABASE_PORT
   - DATABASE_URL is DB-related (contains full connection string)
   - But generic HOST/PORT are for the application server itself

3. If a variable name or its value clearly relates to database hostnames, ports, credentials, 
   connection strings, SSL settings, or database names, classify it as DB-related.
   Include common naming patterns with database prefixes (e.g., DB_USER, PGHOST, DATABASE_URL, MYSQL_PORT).
   Do not classify variables that relate to app-level configuration (e.g., admin users, 
   API keys, secret keys, app modules) unless they directly configure database access.
   
   IMPORTANT: Variables used for client-side API access are NOT database variables:
   - NEXT_PUBLIC_SUPABASE_URL, NEXT_PUBLIC_SUPABASE_ANON_KEY → not_db_related (client-side API access)
   - Variables with NEXT_PUBLIC_ prefix are exposed to the browser and are NOT direct database credentials
   - Supabase variables with PUBLIC, ANON_KEY, or API_URL patterns are for API access, not database connections
   - Only SUPABASE_DB_* or DATABASE_URL patterns pointing to direct Postgres connections are DB-related

4. Contextual references to DB usage (e.g., used in a connection object, ORM config, 
   connection pool setup) are a strong signal — but do not override rules #1, #2, or #3.
   Example: if a variable appears in `psycopg.connect(...)` or `mysql.connector.connect(...)`, 
   that is a strong indication it is DB-related.
   But if HOST appears in `app.listen(HOST, PORT)`, that is application server config, NOT database.

5. Do not guess based on proximity or framework conventions alone.
   - Example: `DJANGO_SUPERUSER_EMAIL` is app auth config, not DB config.  
   - Example: `SECRET_KEY` is cryptography, not DB.  
   - Example: `DEBUG` is runtime, not DB.
   - Example: `HOST` is application server host, not DB (use DB_HOST or PGHOST for database)

REDIS/CACHE CLASSIFICATION RULES:
1. Redis variables should be classified with Redis-specific roles:
   - REDIS_URL, REDIS_CONNECTION_STRING → `redis_uri` (complete Redis connection URL)
   - REDIS_HOST, REDIS_HOSTNAME → `redis_host` (Redis server hostname)
   - REDIS_PORT → `redis_port` (Redis server port)
   - REDIS_PASSWORD, REDIS_AUTH → `redis_password` (Redis authentication)
   - REDIS_DB, REDIS_TLS, REDIS_SSL → `other_redis_config` (other Redis configuration)

2. Variables used in Redis client connections should be classified as Redis-related:
   - Example: if a variable appears in `redis.createClient(...)` or `Redis(...)`, classify as Redis
   - Variables used for cache configuration, session stores backed by Redis, or queue systems (Bull, Kue) backed by Redis

3. IMPORTANT: Generic CACHE_* variables may or may not be Redis:
   - If the context shows Redis usage (e.g., redis package imports, redis:// URLs), classify as Redis
   - If unclear, use `other_redis_config` for cache-related variables when Redis is detected in the project

Database categories:
- `full_uri` – complete database connection URL (e.g., postgres://user:pass@host:port/dbname)
- `hostname` – host or server address for the database
- `port` – database port number
- `username` – database username
- `password` – database password or auth token
- `database_name` – logical DB name
- `other_db_config` – database-related but not fitting above categories

Redis/Cache categories:
- `redis_uri` – complete Redis connection URL (e.g., redis://user:password@host:port/db)
- `redis_host` – Redis server hostname
- `redis_port` – Redis server port
- `redis_password` – Redis authentication password
- `other_redis_config` – Redis-related but not fitting above categories

General:
- `not_db_related` – unrelated to databases or backing services

SENSITIVITY CLASSIFICATION RULES:

Sensitive variables (should be stored using a provider's secret storage functionality):
- API keys, tokens, secrets (e.g., API_KEY, STRIPE_SECRET_KEY, OPENAI_API_KEY)
- Database credentials (passwords, connection strings with credentials)
- Redis credentials (passwords, connection strings with credentials)
- Private keys, certificates (e.g., JWT_SECRET, PRIVATE_KEY, RSA_PRIVATE_KEY)
- OAuth tokens, session secrets (e.g., OAUTH_TOKEN, SESSION_SECRET, NEXTAUTH_SECRET)
- Payment gateway credentials (e.g., STRIPE_SECRET_KEY, PAYPAL_SECRET)
- Authentication tokens (e.g., AUTH_TOKEN, BEARER_TOKEN, ACCESS_TOKEN)
- Encryption keys (e.g., ENCRYPTION_KEY, AES_KEY)

Non-sensitive variables (can be stored as plain environment variables):
- Environment names (e.g., NODE_ENV=production, ENVIRONMENT=staging)
- Port numbers (e.g., PORT=8080, SERVER_PORT=3000)
- Public URLs (e.g., API_URL=https://api.example.com, PUBLIC_URL=https://example.com)
- Feature flags (e.g., ENABLE_FEATURE_X=true, DEBUG_MODE=false)
- Log levels (e.g., LOG_LEVEL=info, LOGGING_ENABLED=true)
- Public API endpoints (e.g., NEXT_PUBLIC_API_URL, PUBLIC_API_ENDPOINT)
- Build-time configuration (e.g., BUILD_ID, VERSION, COMMIT_SHA)

IMPORTANT for sensitivity detection:
- Base your decision on variable NAME patterns (e.g., _KEY, _SECRET, _TOKEN, _PASSWORD suffixes)
- Consider variable USAGE in code context (e.g., used in authentication headers, encryption)
- Understand framework conventions (e.g., NEXT_PUBLIC_ prefix means client-side, NOT sensitive)
- NEVER make assumptions about the actual value - only analyze the name and usage pattern
- When in doubt, mark as sensitive (better safe than sorry)

{{ _.role("user") }}
The project uses the following backing services: {{dbList}}.

Variable name: {{envVar.varName}}
File it was found in: {{envVar.file}}
Line it was found on: {{envVar.line}}
Code snippet:
  {{envVar.context}}

For the given environment variable, determine:
- Is this variable related to database or Redis/cache configuration? (true/false)  
- Always provide a **Role** value:  
  - If DB-related → exactly one from the database categories.
  - If Redis-related → exactly one from the Redis/cache categories.
  - If neither → return `not_db_related`.  
- Which service type? (choose from {{dbList}} or "unknown")
- Is this variable sensitive? (true/false)
- Provide a brief reason for the sensitivity classification

Examples:

DATABASE_URL (used in db connection) → DB: full_uri, postgresql, SENSITIVE: true, "Contains database credentials in connection string"
PGHOST (used for postgres host) → DB: hostname, postgresql, SENSITIVE: false, "Database hostname is configuration, not a secret"  
MYSQL_PORT (port number) → DB: port, mysql, SENSITIVE: false, "Port number is public configuration"
DB_PASSWORD (database auth) → DB: password, unknown, SENSITIVE: true, "Database password credential"
REDIS_URL (used in redis connection) → REDIS: redis_uri, redis, SENSITIVE: true, "Contains Redis credentials in connection string"
REDIS_HOST (redis server) → REDIS: redis_host, redis, SENSITIVE: false, "Redis hostname is configuration, not a secret"
REDIS_PORT (redis port) → REDIS: redis_port, redis, SENSITIVE: false, "Port number is public configuration"
REDIS_PASSWORD (redis auth) → REDIS: redis_password, redis, SENSITIVE: true, "Redis password credential"
CACHE_URL (redis cache) → REDIS: redis_uri, redis, SENSITIVE: true, "Cache connection string with Redis"
STRIPE_SECRET_KEY (payment API) → DB: not_db_related, SENSITIVE: true, "Payment gateway API secret key"
OPENAI_API_KEY (LLM API) → DB: not_db_related, SENSITIVE: true, "Third-party API key"
JWT_SECRET (auth tokens) → DB: not_db_related, SENSITIVE: true, "Used for signing authentication tokens"
NODE_ENV (environment) → DB: not_db_related, SENSITIVE: false, "Runtime environment identifier - not sensitive"
PORT (server port) → DB: not_db_related, SENSITIVE: false, "Server port number - public configuration"
API_URL (public endpoint) → DB: not_db_related, SENSITIVE: false, "Public API endpoint URL"
NEXT_PUBLIC_API_URL (client-side) → DB: not_db_related, SENSITIVE: false, "Client-side public URL - exposed to browser"
SECRET_KEY (cryptography) → DB: not_db_related, SENSITIVE: true, "Cryptographic secret for encryption/signing"
DJANGO_SUPERUSER_EMAIL (app config) → DB: not_db_related, SENSITIVE: false, "Application configuration - not a secret"
NEXT_PUBLIC_SUPABASE_URL (client API) → DB: not_db_related, SENSITIVE: false, "Client-side API URL - public"
NEXT_PUBLIC_SUPABASE_ANON_KEY (client key) → DB: not_db_related, SENSITIVE: false, "Anonymous key for client-side - designed to be public"
    {{ctx.output_format }}
  "#

  }

  function CategorizeRoutes(candidates: RouteCandidate[]) ->  CategorizedRoutes {
  client "ProxyClient"
  prompt #"
{{_.role("system")}}
You are an assistant that analyzes source code snippets to identify web routes in an application.

You will receive a list of candidate routes, each with:

- The HTTP method
- The path
- The file name and line number
- A snippet of code (the “context”) showing how the route is implemented

Your job is to:
Categorize each route as one of:

- homepage
- health
- api
- static_asset
- admin
- auth
- error
- other

Assign a confidence score between 0.0 and 1.0 for how well the route fits that category.
Choose the most appropriate default route to probe in order to confirm a newly deployed 
app is live. General rules:

Prefer a root (/) or homepage route if one exists.
Otherwise prefer a health/status route.
Otherwise prefer a GET route likely to return something harmless (like /index.html or a read-only API).

Avoid routes that look destructive (/delete, /reset, /admin/*, etc.).

Return results in structured JSON.
{{ _.role("user") }}

Here are the route candidates from the deployed code:
{{candidates}}

    {{ctx.output_format }}
  "#

  }

function DetermineBuildOutput(candidate: BuildOutputCandidate) -> BuildOutput {
  client "ProxyClient"
  prompt #"
{{_.role("system")}}
You are an expert software deployment assistant. 
You are given information about a project's build output configuration, including a default directory detected by static analysis. 
Your job is to determine the correct build output directory for production deployment.

- First, check the configuration contents for an explicit output directory override.
- If an override is found, return that directory.
- If not, return the provided default directory.
- If neither is available, return a safe generic default such as "dist" or "build".
- Respond only with the final output directory path (e.g. ".next", "dist", "build").

{{_.role("user")}}
Framework: {{candidate.framework}}
Source: {{candidate.source}}
DefaultPath: {{candidate.default}}
ConfigContents: {{candidate.context}}

{{ctx.output_format}}

"#
}
function DetermineLaunchCommand(language: string, frameworks: string[], envVars: string[], lc: LaunchContext) -> LaunchCommand {
  client "ProxyClient"
  prompt #"
{{_.role("system")}}
You are an expert in analyzing project launcher files to determine both:

1. Whether the application produces a purely static build that can be hosted directly without running a server (`command` will be an empty string).
2. If it requires a server runtime, the exact shell command a developer would run in production to start the server (`command` will be set).

Your output must be ONLY valid JSON with the following shape:
    {{ctx.output_format }}


Rules:

Python projects:
- ALWAYS prefer running a main launcher script if one exists (e.g. run.py, main.py, app.py at project root).
- If a project includes a Python launcher script that calls uvicorn/gunicorn/flask/etc, prefer running it with `python <script_name>`.
- Only fall back to framework-specific commands (flask run, uvicorn, etc.) if no main launcher script exists.
- If the README shows a uvicorn/gunicorn/flask command, you may include that as an alternative,
  but adapt it to respect environment variables instead of hardcoding.
- Do not hardcode values already provided by env vars or configs (e.g. HOST, PORT, RELOAD).
- Prefer production-ready commands. Do not include flags intended only for development (e.g. --reload, --debug).

Node/JavaScript projects:
- Only use commands that appear in the `scripts` section of `package.json`. 
  Do NOT invent commands that are not explicitly defined.
- Prefer production-ready scripts such as `start:prod`, `serve:ssr`, or equivalents over plain `start`.
- If only `start` exists, you may use it, but otherwise favor scripts that clearly indicate production usage.
- Do not expand npm/yarn/pnpm scripts into their underlying commands — run the script as defined.
- For monorepos or workspaces, use the appropriate workspace syntax if evident (e.g. `npm run start --workspace=frontend`).
- Do NOT use `preview` scripts (e.g. `astro preview`, `vite preview`) for production. These are only for locally testing a static build.
- If the only scripts are `build` and `preview` (common in Astro, Vite, Gatsby, Angular without SSR, etc.), 
  treat the project as a static-site build (`"command": ""`).
- If the project includes an SSR/server runtime script (e.g. `serve:ssr`, `node dist/server.js`), 
  infer the correct `"command"` using the defined script.

{{ _.role("user") }}

The project may use the following frameworks: {{frameworks}}
The project uses the following environment variables: {{envVars}}

  {% if lc.launchers | length > 0 %}
  Here are the candidate launcher files:
  {% for launcher in lc.launchers %}
  File: {{launcher.name}}
  ---
  {{launcher.content}}
  ---
  {% endfor %}
  {% endif %}

  {% if lc.readme and lc.readme | trim != "" %}
  Here is the README:
  ---
  {{lc.readme}}
  ---
  {% endif %}

  Based on this information, infer the correct command to start the application.
  Examples of possible commands include:
  - uvicorn app.main:app --reload
  - python manage.py runserver
  - gunicorn myproject.wsgi:application
  - flask run

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
      dryRun false
    }
    name "foo"
    language ""
    detectedPlatforms []
  }
}



test errorSummary {
  functions [SummarizeDeployError]
  args {
    intent {
      action "DEPLOY"
      platform "Render"
      source "/path/to/app"
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

test envVarRoles {
  functions [DetermineEnvVarRoles]
  args {
    envVar {
      varName "DATABASE_URL"
      file "/path/to/app/.env"
      line 42
      context "DATABASE_URL=postgres://user:password@localhost:5432/mydb"
    }
    dbList ["PostgreSQL", "MySQL", "MongoDB"]
  }
}

class MigrationCommand {
  command string
  confidence string // "high", "medium", "low"
  explanation string
}

class MigrationFile {
  path string
  type string // "migration", "config", "schema"
}

class MigrationContext {
  migrationFiles MigrationFile[]
  ormTools string[]
  packageScripts string @description("JSON string of package.json scripts or similar")
  configSnippets string @description("Relevant config file snippets")
}

function DetermineMigrationCommand(
  language: string,
  frameworks: string[],
  ormTools: string[],
  migrationContext: MigrationContext
) -> MigrationCommand {
  client "ProxyClient"
  prompt #"
{{_.role("system")}}
You are an expert in database migration tools and ORMs across different programming languages.
Your task is to determine the production database migration command for a project.

CRITICAL VALIDATION RULES:
1. Only suggest a migration command if BOTH conditions are met:
   a) The migration tool is listed in dependencies/ormTools
   b) The migration tool has actual configuration files or migration directories present
2. Simply having a tool in dependencies is NOT sufficient - you must see evidence of actual migration setup
3. If you see an ORM tool (e.g. alembic, sqlalchemy) but NO migration files or config, return EMPTY command
4. If migrationContext.migrationFiles is empty or only contains generic directories, return EMPTY command
5. When in doubt, return an empty command - false positives are worse than false negatives

Evidence Requirements by Tool:
- Alembic: Requires alembic.ini OR alembic/versions directory
- Django: Requires manage.py AND migrations directories
- Flask-Migrate: Requires migrations/ directory with alembic.ini inside
- Prisma: Requires prisma/migrations directory OR prisma/schema.prisma
- TypeORM: Requires migration files OR ormconfig with migrations path
- Sequelize: Requires migrations/ directory
- Custom migrations: If "custom" is in ormTools, check packageScripts and configSnippets for migration commands

Special Case - Custom Migrations:
When ormTools includes "custom", this indicates a project with custom migration scripts.
Look for:
1. Package.json scripts with migration keywords (db:migrate, migrate, etc.)
2. Custom migration files like migrate.ts, migrate.js, schema.sql
3. The command should use the npm script name (e.g., "npm run db:migrate")

Common migration patterns by language and tool:

**Node.js/JavaScript:**
- Prisma: `npx prisma migrate deploy` (production) or `npx prisma db push` (development)
- TypeORM: `npm run typeorm migration:run` or `npx typeorm migration:run`
- Sequelize: `npx sequelize-cli db:migrate` or `npm run db:migrate`
- Knex: `npx knex migrate:latest` or `npm run knex migrate:latest`
- Mikro-ORM: `npx mikro-orm migration:up`
- Drizzle: `npm run db:migrate` or `npx drizzle-kit migrate` (check package.json scripts first)
- db-migrate: `npx db-migrate up`
- Custom: If package.json has a migration script (e.g., "db:migrate"), use `npm run <script-name>`

**Python:**
- Django: `python manage.py migrate` or `./manage.py migrate`
- Alembic (SQLAlchemy): `alembic upgrade head`
- Flask-Migrate: `flask db upgrade` or `python -m flask db upgrade`
- Peewee: `python migrate.py`
- Tortoise ORM: `aerich upgrade`

**Ruby:**
- Rails: `rails db:migrate` or `bundle exec rake db:migrate`
- Sequel: `sequel -m migrations postgres://...`
- Hanami: `hanami db migrate`

**Java/JVM:**
- Flyway: `flyway migrate`
- Liquibase: `liquibase update`

Rules for determining the command:
1. ALWAYS prefer commands that appear in package.json scripts (for Node.js projects)
   - If you see "db:migrate" in packageScripts, return "npm run db:migrate"
   - If you see "migrate" in packageScripts, return "npm run migrate"
   - This is the HIGHEST priority for Node.js projects
2. Use production-safe commands (e.g., `prisma migrate deploy` not `prisma db push`)
3. Include necessary prefixes (npx, bundle exec, python, etc.)
4. Don't include environment-specific flags (--env=production, etc.)
5. Don't include connection strings or credentials
6. Return empty command if no clear migration tool is found

Output confidence levels:
- "high": Clear migration tool detected with standard patterns
- "medium": Migration files found but command pattern unclear
- "low": Possible migrations but very uncertain
- Return empty command if confidence would be below "low"

{{ _.role("user") }}
Language: {{ language }}
Frameworks: {{ frameworks }}
ORM/Migration Tools Detected: {{ ormTools }}
Migration Context: {{ migrationContext }}

Analyze the project and determine the production migration command.
If package.json scripts are provided, prefer using those script names with npm/yarn.
Return empty command if you cannot determine a migration command with reasonable confidence.

{{ctx.output_format }}
"#
}
