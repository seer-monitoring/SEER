# Import SEER monitoring
from seerpy import Seer

seer = Seer(apiKey='df_b1c79d118073b82f9aa961458253057f208d9d32d388df91a46343bbeca079b4')
# Your existing imports
import pandas as pd
import requests

# def run_report():
#     # Your script logic here
#     data = pd.read_csv("sales_data.csv")
#     print(data)
#     #processed = data.groupby("region").sum()
#     data.to_csv("daily_report.csv")

# # Wrap your script with SEER monitoring
# with seer.monitor("a",True,metadata={'run_type':'ADHOC'}):
#     run_report() # + your script logic here
seer.heartbeat("a",metadata={'run_type':'ADHOC'})   