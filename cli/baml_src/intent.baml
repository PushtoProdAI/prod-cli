

class Intent {
    action string
    platform string
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
    {{ _.role("user") }}
    {{ request }}

    {{ ctx.output_format }}
  "#
}

test testIntent {
  functions [ExtractIntent]
  args {
    request #"
     Push this app to fly.
    "#
  }
}

