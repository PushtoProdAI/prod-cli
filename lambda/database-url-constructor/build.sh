#!/bin/bash
set -e

echo "Building Lambda function package..."

# Clean previous build
rm -rf node_modules function.zip

# Install production dependencies
npm install --production

# Create zip file
zip -r function.zip index.js package.json node_modules/ -x "*/.*" "node_modules/.bin/*"

echo "✓ Built function.zip ($(du -h function.zip | cut -f1))"
echo ""
echo "Next steps:"
echo "1. Upload to S3: aws s3 cp function.zip s3://YOUR_BUCKET/lambda-functions/database-url-constructor/function.zip"
echo "2. Make it publicly readable (optional): aws s3api put-object-acl --bucket YOUR_BUCKET --key lambda-functions/database-url-constructor/function.zip --acl public-read"
