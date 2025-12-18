# Import MCP library for proper result handling
import mcp

# Import our string utilities library - dynamically loaded from libraries_path
import string_utils

def process_text(operation, text):
    """Process text using the string utilities library"""

    if operation == "reverse":
        return string_utils.reverse_string(text)
    elif operation == "uppercase":
        return string_utils.to_uppercase(text)
    elif operation == "lowercase":
        return string_utils.to_lowercase(text)
    elif operation == "capitalize":
        return string_utils.capitalize_words(text)
    elif operation == "count_words":
        count = string_utils.count_words(text)
        return f"The text has {count} word(s)"
    elif operation == "remove_spaces":
        return string_utils.remove_spaces(text)
    elif operation == "is_palindrome":
        result = string_utils.is_palindrome(text)
        if result:
            return f"'{text}' is a palindrome!"
        else:
            return f"'{text}' is not a palindrome."
    else:
        return f"Error: Unknown operation '{operation}'. Available operations: reverse, uppercase, lowercase, capitalize, count_words, remove_spaces, is_palindrome"

# Get parameters using mcp.get()
operation = mcp.get("operation")
text = mcp.get("text")

result = process_text(operation, text)

# Return the result using MCP library
mcp.return_string(str(result))