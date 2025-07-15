

class Intent {
    action string
    platform string
    source string
  }

class IntentSummary {
    summary string
  }

function ExtractIntent(request: string) -> Intent {
  client "OllamaClient"
  prompt #"
 {{ _.role("system") }}
     Classify this user request into one of these intents:
    - DEPLOY: Deploy application to cluster
    - STATUS: Check deployment status  
    - ROLLBACK: Rollback deployment
    - SCALE: Scale deployment
    - DELETE: Remove deployment.

    If it not one of these, return "UNKNOWN". As the action. 

    You will also extract the platform from the request, which can be one of these:
     - Render
     - Fly.io

    If it does not mention a platform, return "UNKNOWN" as the platform. 
    
    You MUST NOT guess if you don't know. For example, we do not support Kubernetes, so do not guess if you see it.
    This applies to ANY platform that is not listed above.

    Finally, extract the source from the prompt. Source should be in the form of a file path. If you cannot determine one, don't guess
    and just set it to an empty string

    {{ _.role("user") }}
    {{ request }}

    {{ ctx.output_format }}
  "#
}

function SummarizeIntent(intent: Intent, name: string, language: string) -> IntentSummary {
  client "OllamaClient"
  prompt #"
    {{ _.role("system") }}
    You are an agent who is tasked for managing applications on a Platform as A Service (Paas). You will be provided
    with what the user intends to do, the platform, the source of the project on their filesystem, the name of the project. Please provide a summary
    so that the user can approve or refine their request before it is triggered on the platform.
    
    If any properties of the intent are UNKNOWN, you must prompt the user to refine their request.

    If name or language are blank or not provided, you must prompt the user to provide a name and language for the project.
    
    Summarize this in a few sentences that can be understood by someone who may not be a very technical developer. Use a friendly tone.
    

    {{ _.role("user") }}

    Intent: {{ intent }}
    Language: {{ language }}
    Name: {{ name }}

 
    {{ctx.output_format }}
  "#
}


test testIntent {
  functions [ExtractIntent]
  args {
    request #"
     Push this app at /path/to/app to fly.
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
    language "  "
  }
}

