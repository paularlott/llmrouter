import scriptling.mcp.tool as tool

SALES = {
    "eu": {"units": 1240, "revenue": 48210.0},
    "us": {"units": 1810, "revenue": 91500.0},
    "apac": {"units": 960, "revenue": 33120.0},
}

region = tool.get_string("region", "all")

if region == "all":
    rows = list(SALES.items())
else:
    rows = [(region, SALES.get(region, {"units": 0, "revenue": 0.0}))]

units = sum(r["units"] for _, r in rows)
revenue = sum(r["revenue"] for _, r in rows)
tool.return_string(
    "region=" + region + " units=" + str(units) + " revenue=" + str(round(revenue, 2))
)
