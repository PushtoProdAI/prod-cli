import "jsr:@supabase/functions-js/edge-runtime.d.ts"
import { createClient } from "jsr:@supabase/supabase-js@2";
import { ecrTokenRequest } from "../_shared/aws.ts";
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts';

// Initialize Sentry
initSentry();


Deno.serve(async (req) => {
  try {
    if (req.method !== 'POST') {
    return new Response(
      JSON.stringify({ error: 'Method not allowed' }),
      { 
        status: 405, 
        headers: { "Content-Type": "application/json" } 
      }
    )
  }

    const supabaseClient = createClient(
    Deno.env.get('SUPABASE_URL') ?? '',
    Deno.env.get('SUPABASE_ANON_KEY') ?? '',
    {
      global: {
        headers: { Authorization: req.headers.get('Authorization')! },
      },
    },
  );



  const authHeader = req.headers.get('Authorization')!;
  const token = authHeader.replace('Bearer ', '');
  const { data } = await supabaseClient.auth.getUser(token);

  const { name } = await req.json();

  if (!name) {
    return new Response("Missing name", { status: 400 });
  }

  const roleArn = Deno.env.get("AWS_PULL_ROLE_ARN");
  const result = await ecrTokenRequest(data.user.id, name, roleArn);

  if (result instanceof Error) {
    console.error("Pull token error:", result);
    captureException(result, {
      function: 'pull-token',
      operation: 'ecr_token_request',
      user_id: data?.user?.id,
      repo_name: name
    });
    return new Response(result.message, { status: 500 });
  }

  return Response.json(result);
  
  } catch (error) {
    console.error('Unexpected error in pull-token function:', error);
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'pull-token',
      operation: 'general_error',
      method: req.method
    });
    await flushSentry();
    
    return new Response(
      JSON.stringify({ error: 'Internal server error' }),
      { status: 500, headers: { 'Content-Type': 'application/json' } }
    );
  }
});

