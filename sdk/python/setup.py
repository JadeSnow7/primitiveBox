from setuptools import setup, find_packages

setup(
    name="primitivebox",
    version="0.1.0",
    description="Python SDK for PrimitiveBox — AI-Native Containerized Development Platform",
    long_description=open("../../README.md").read() if __import__("os").path.exists("../../README.md") else "",
    long_description_content_type="text/markdown",
    author="PrimitiveBox Team",
    license="Apache-2.0",
    packages=find_packages(),
    python_requires=">=3.9",
    install_requires=[],  # Zero dependencies for MVP (stdlib only)
    extras_require={
        "async": ["aiohttp>=3.9"],
        "dev": ["pytest", "pytest-asyncio"],
    },
    classifiers=[
        "Development Status :: 3 - Alpha",
        "Intended Audience :: Developers",
        "License :: OSI Approved :: Apache Software License",
        "Programming Language :: Python :: 3",
        "Topic :: Software Development :: Libraries",
    ],
)
