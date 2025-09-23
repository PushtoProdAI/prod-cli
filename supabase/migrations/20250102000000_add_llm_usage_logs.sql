-- Create llm_usage_logs table for tracking LLM usage statistics
CREATE TABLE IF NOT EXISTS llm_usage_logs (
  id UUID DEFAULT gen_random_uuid() PRIMARY KEY,
  user_id TEXT NOT NULL,
  function_name TEXT NOT NULL,
  model_used TEXT NOT NULL,
  tokens_used INTEGER DEFAULT 0,
  cost DECIMAL(10, 6) DEFAULT 0.0,
  response_time_ms INTEGER DEFAULT 0,
  success BOOLEAN DEFAULT true,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Create index for efficient querying by user and date
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_user_date ON llm_usage_logs(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_created_at ON llm_usage_logs(created_at);

-- Enable RLS (Row Level Security)
ALTER TABLE llm_usage_logs ENABLE ROW LEVEL SECURITY;

-- Create policy to allow users to see their own usage logs
CREATE POLICY "Users can view their own usage logs" ON llm_usage_logs
  FOR SELECT USING (auth.uid()::text = user_id);

-- Create policy to allow service role to insert usage logs
CREATE POLICY "Service role can insert usage logs" ON llm_usage_logs
  FOR INSERT WITH CHECK (true);
