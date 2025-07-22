import "jsr:@supabase/functions-js/edge-runtime.d.ts"

import {
  ecrTokenRequest,
} from "../_shared/aws.ts";


Deno.serve(async (req) => {
  // TODO: get this from the auth header/supabase auth instead
  const { tenantId } = await req.json();

  if (!tenantId) {
    return new Response("Missing tenantId", { status: 400 });
  }

  const roleArn = Deno.env.get("AWS_PULL_ROLE_ARN");
  const result = await ecrTokenRequest(tenantId, roleArn);

  if (result instanceof Error) {
    console.error("Pull token error:", result);
    return new Response(result.message, { status: 500 });
  }

  return Response.json(result);
});

