from setuptools import setup, find_packages

setup(
    name='seerpy',
    version='0.1.5',
    description='A lightweight monitoring and heartbeat client for Seer API',
    long_description=open('README.md', encoding='utf-8').read(),
    long_description_content_type='text/markdown',
    url='https://github.com/xlor1009/seer',
    author='SEER',
    author_email='support@mg.ansrstudio.com',
    license='LicenseRef-Seer-4.2',
    packages=find_packages(),
    python_requires='>=3.8',
    install_requires=[
        'requests',
        'python-dotenv'
    ],
    classifiers=[
        'Development Status :: 1 - Planning',
        'Intended Audience :: Science/Research',
        'Programming Language :: Python :: 3',
        'Operating System :: OS Independent'
    ],
    include_package_data=True,
    # If you want to include LICENSE explicitly in the wheel/sdist
    data_files=[('', ['LICENSE'])]
)
