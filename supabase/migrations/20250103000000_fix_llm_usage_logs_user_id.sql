-- Fix user_id column in llm_usage_logs table to be a proper foreign key
-- This migration corrects the user_id column to match the pattern used in other tables

-- First, drop the existing index that uses the old column
DROP INDEX IF EXISTS idx_llm_usage_logs_user_date;

-- Drop all existing RLS policies that reference the old column
DROP POLICY IF EXISTS "Users can view their own usage logs" ON llm_usage_logs;
DROP POLICY IF EXISTS "user_usage_logs_policy" ON llm_usage_logs;
DROP POLICY IF EXISTS "Service role can insert usage logs" ON llm_usage_logs;

-- Delete any rows with invalid user_id values (like 'anonymous')
DELETE FROM llm_usage_logs WHERE user_id = 'anonymous' OR user_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';

-- Alter the user_id column to be a proper UUID foreign key
ALTER TABLE llm_usage_logs 
  ALTER COLUMN user_id TYPE UUID USING user_id::UUID,
  ADD CONSTRAINT fk_llm_usage_logs_user_id 
    FOREIGN KEY (user_id) REFERENCES auth.users(id) ON DELETE CASCADE;

-- Recreate the index with the correct column type
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_user_date ON llm_usage_logs(user_id, created_at);

-- Recreate the RLS policies with the correct column type
CREATE POLICY "Users can view their own usage logs" ON llm_usage_logs
  FOR SELECT USING (auth.uid() = user_id);

CREATE POLICY "Service role can insert usage logs" ON llm_usage_logs
  FOR INSERT WITH CHECK (true);
